package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// PlacementResult describes which Executor was selected and why.
type PlacementResult struct {
	Target string `json:"target"`
	Reason string `json:"reason"`
}

// PlacementError is returned when no Executor can fit the model.
type PlacementError struct {
	EstimatedVRAMGB float64
	Candidates      []PlacementCandidate
}

func (e *PlacementError) Error() string {
	if len(e.Candidates) == 0 {
		return fmt.Sprintf("no Executor nodes available in the mesh")
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("no Executor has sufficient VRAM (need %.1f GB)", e.EstimatedVRAMGB))
	for _, c := range e.Candidates {
		lines = append(lines, fmt.Sprintf("  %s: %.1f GB total VRAM", c.Name, c.VRAMTotalGB))
	}
	return strings.Join(lines, "\n")
}

// PlacementCandidate is an Executor considered during placement.
type PlacementCandidate struct {
	Name        string
	VRAMTotalGB float64
	DiskFreeGB  float64
	HasModel    bool
}

// EstimateModelVRAM queries the HF API for model file sizes and estimates VRAM requirement.
// Formula: GGUF = file_size × 1.1; safetensors/mlx = sum of weight files × 1.1.
func EstimateModelVRAM(repoID, format, quant string) (float64, error) {
	sizeBytes, err := queryModelSize(repoID, format, quant)
	if err != nil {
		return 0, err
	}
	// Apply 1.1x overhead multiplier.
	estimatedBytes := float64(sizeBytes) * 1.1
	return estimatedBytes / (1024 * 1024 * 1024), nil // Convert to GB.
}

// queryModelSize queries the HF API for the relevant file sizes without downloading.
func queryModelSize(repoID, format, quant string) (int64, error) {
	apiURL := fmt.Sprintf("https://huggingface.co/api/models/%s", repoID)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return 0, fmt.Errorf("creating HF API request: %w", err)
	}
	if token := os.Getenv("HF_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if token := os.Getenv("HUGGING_FACE_HUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("querying HF API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("HF API returned %d for %s", resp.StatusCode, repoID)
	}

	var repoInfo struct {
		Siblings []struct {
			Filename string `json:"rfilename"`
			Size     int64  `json:"size"`
		} `json:"siblings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&repoInfo); err != nil {
		return 0, fmt.Errorf("decoding HF API response: %w", err)
	}

	if format == "gguf" {
		return sizeForGGUF(repoInfo.Siblings, quant, repoID)
	}
	return sizeForSnapshot(repoInfo.Siblings, format)
}

// sizeForGGUF finds the matching GGUF file size.
func sizeForGGUF(siblings []struct {
	Filename string `json:"rfilename"`
	Size     int64  `json:"size"`
}, quant, repoID string) (int64, error) {
	quantLower := strings.ToLower(quant)
	for _, f := range siblings {
		if !strings.HasSuffix(strings.ToLower(f.Filename), ".gguf") {
			continue
		}
		if strings.Contains(strings.ToLower(f.Filename), quantLower) {
			if f.Size > 0 {
				return f.Size, nil
			}
		}
	}
	// If no size from API, fall back to HEAD request for the specific file.
	return 0, fmt.Errorf("could not determine file size for %s (quant=%s) from HF API", repoID, quant)
}

// sizeForSnapshot sums weight file sizes for mlx/safetensors formats.
func sizeForSnapshot(siblings []struct {
	Filename string `json:"rfilename"`
	Size     int64  `json:"size"`
}, format string) (int64, error) {
	var total int64
	for _, f := range siblings {
		name := strings.ToLower(f.Filename)
		if isWeightFile(name, format) {
			total += f.Size
		}
	}
	if total == 0 {
		return 0, fmt.Errorf("could not determine model size: no weight files found")
	}
	return total, nil
}

// isWeightFile returns true if the filename is a model weight file for the given format.
func isWeightFile(name, format string) bool {
	switch format {
	case "safetensors":
		return strings.HasSuffix(name, ".safetensors")
	case "mlx":
		// MLX models use .safetensors or .npz weight files.
		return strings.HasSuffix(name, ".safetensors") || strings.HasSuffix(name, ".npz")
	}
	return false
}

// SelectExecutor picks the best Executor node for a model pull using placement logic.
// It requires mesh state (nodes) and inventory info to make the decision.
//
// Logic:
//  1. Filter to Executor nodes with sufficient VRAM.
//  2. Prefer nodes that already have the model.
//  3. Prefer nodes not currently serving (future-proofing, always true for now).
//  4. Among remaining, prefer most free disk space.
func SelectExecutor(nodes []MeshNode, estimatedVRAMGB float64, repoID, format, quant string, inventoryByNode map[string][]InventoryEntry) (*PlacementResult, error) {
	// Step 1: Find all Executor nodes.
	var executors []MeshNode
	for _, n := range nodes {
		if n.Status == StatusOffline {
			continue
		}
		if HasRole(n.Roles, "executor") {
			executors = append(executors, n)
		}
	}

	if len(executors) == 0 {
		return nil, &PlacementError{EstimatedVRAMGB: estimatedVRAMGB}
	}

	// Step 2: Filter by VRAM capacity.
	var candidates []struct {
		Node     MeshNode
		HasModel bool
	}
	var insufficientCandidates []PlacementCandidate

	for _, n := range executors {
		vramTotal := float64(0)
		if n.GPU != nil {
			vramTotal = n.GPU.VRAMTotalGB
		}
		if vramTotal < estimatedVRAMGB {
			insufficientCandidates = append(insufficientCandidates, PlacementCandidate{
				Name:        n.Name,
				VRAMTotalGB: vramTotal,
				DiskFreeGB:  n.DiskFreeGB,
			})
			continue
		}
		// Check if this node already has the model.
		hasModel := nodeHasModel(inventoryByNode[n.Name], repoID, format, quant)
		candidates = append(candidates, struct {
			Node     MeshNode
			HasModel bool
		}{n, hasModel})
	}

	if len(candidates) == 0 {
		return nil, &PlacementError{
			EstimatedVRAMGB: estimatedVRAMGB,
			Candidates:      insufficientCandidates,
		}
	}

	// Step 3: Sort by preference:
	//   - already has model (descending)
	//   - most free disk space (descending)
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.HasModel && !best.HasModel {
			best = c
		} else if c.HasModel == best.HasModel && c.Node.DiskFreeGB > best.Node.DiskFreeGB {
			best = c
		}
	}

	reason := "most free disk space"
	if best.HasModel {
		reason = "already has model on disk"
	}

	return &PlacementResult{
		Target: best.Node.Name,
		Reason: reason,
	}, nil
}

// HasRole checks if a node has a specific role.
func HasRole(roles []string, role string) bool {
	for _, r := range roles {
		if strings.EqualFold(r, role) {
			return true
		}
	}
	return false
}

// nodeHasModel checks if a node's inventory contains the given model.
func nodeHasModel(entries []InventoryEntry, repoID, format, quant string) bool {
	target := &InventoryEntry{RepoID: repoID, Format: format, Quant: strings.ToUpper(quant)}
	targetKey := target.Key()
	for _, e := range entries {
		if e.Key() == targetKey {
			return true
		}
	}
	return false
}
