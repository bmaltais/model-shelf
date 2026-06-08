package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
	"github.com/alexziskind1/model-shelf/internal/resolver"
)

func cmdPull(args []string) int {
	positional, flags := parseFlags(args)
	jsonMode := flags["json"] == "true"

	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf pull <repo_id> [--target <node>] [--quant Q] [--format F] [--source S] [--force] [--json]")
		fmt.Println()
		fmt.Println("Pull a model from Hugging Face to a target node.")
		fmt.Println("If --target is omitted, auto-selects the best Executor based on VRAM capacity.")
		fmt.Println("Fire-and-forget: returns a job ID immediately.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --target <node>    Target node name (auto-selects if omitted)")
		fmt.Println("  --quant <Q>        Quantization level (required for GGUF)")
		fmt.Println("  --format <F>       Force format: gguf, mlx, safetensors")
		fmt.Println("  --source <S>       Prefer source: auto (default), hf, peer")
		fmt.Println("  --force            Delete and re-transfer if model already exists")
		fmt.Println("  --json             Emit JSON output")
		return 0
	}

	if len(positional) < 1 {
		emitPullError(jsonMode, "usage: model-shelf pull <repo_id> [--target <node>] [--quant Q] [--format F] [--json]")
		return 1
	}
	repoID := positional[0]
	target := flags["target"]

	// Detect format.
	format := flags["format"]
	if format == "" {
		format = resolver.DetectFormat(repoID)
	}

	// Validate format.
	valid := false
	for _, f := range resolver.SupportedFormats {
		if f == format {
			valid = true
			break
		}
	}
	if !valid {
		emitPullError(jsonMode, fmt.Sprintf("unsupported format: %q", format))
		return 1
	}

	quant := flags["quant"]
	if format == "gguf" && quant == "" {
		emitPullError(jsonMode, "--quant is required for gguf format")
		return 1
	}

	// If no target specified, check local shelf first before smart placement.
	if target == "" && flags["force"] != "true" {
		if cfg, err := meshconfig.Load(); err == nil && cfg.ShelfRoot != "" {
			localResult, err := resolver.ResolveModel(&resolver.Config{
				ShelfRoot:      cfg.ShelfRoot,
				AllowDownloads: false,
			}, repoID, format, quant)
			if err == nil && localResult.Status == "found" {
				localName := cfg.Name
				if localName == "" {
					if h, err := os.Hostname(); err == nil {
						localName = h
					}
				}
				if jsonMode {
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					enc.Encode(daemon.PullResponse{
						Status: daemon.JobAlreadyPresent,
						Target: localName,
					})
				} else {
					fmt.Printf("  status    %s\n", daemon.JobAlreadyPresent)
					fmt.Printf("  target    %s\n", localName)
				}
				return 0
			}
		}
	}

	// If no target specified, use smart placement to auto-select an Executor.
	var placementResult *daemon.PlacementResult
	if target == "" {
		result, err := autoSelectTarget(repoID, format, quant)
		if err != nil {
			emitPullError(jsonMode, err.Error())
			return 1
		}
		target = result.Target
		placementResult = result
	}

	// Look up the target node's address from mesh state.
	addr, port, err := resolveTargetNode(target)
	if err != nil {
		emitPullError(jsonMode, err.Error())
		return 1
	}

	// Load mesh key for auth.
	meshKey := loadMeshKey()

	// Send POST /v1/pull to the target.
	sourceFlag := flags["source"]
	if sourceFlag == "" {
		sourceFlag = "auto"
	}
	switch sourceFlag {
	case "auto", "hf", "peer":
		// valid
	default:
		emitPullError(jsonMode, "--source must be one of: auto, hf, peer")
		return 1
	}
	pullReq := daemon.PullRequest{
		RepoID:       repoID,
		Format:       format,
		Quant:        quant,
		Force:        flags["force"] == "true",
		PreferSource: sourceFlag,
	}
	body, err := json.Marshal(pullReq)
	if err != nil {
		emitPullError(jsonMode, err.Error())
		return 1
	}

	url := fmt.Sprintf("http://%s/v1/pull", net.JoinHostPort(addr, fmt.Sprintf("%d", port)))
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		emitPullError(jsonMode, err.Error())
		return 1
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if meshKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+meshKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		if jsonMode {
			emitPullError(jsonMode, fmt.Sprintf("cannot reach node %q — daemon may be offline", target))
		} else {
			fmt.Fprintf(os.Stderr, "error: cannot reach node %q — daemon may be offline\n", target)
			if hint := pullConnectionHint(err, target); hint != "" {
				fmt.Fprintf(os.Stderr, "  hint: %s\n", hint)
			}
		}
		return 1
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		emitPullError(jsonMode, fmt.Sprintf("reading response: %v", err))
		return 1
	}

	if resp.StatusCode != http.StatusAccepted {
		endpoint := net.JoinHostPort(addr, fmt.Sprintf("%d", port))
		var errResp map[string]string
		if json.Unmarshal(respBody, &errResp) == nil && errResp["error"] != "" {
			emitPullError(jsonMode, fmt.Sprintf("pull failed — target %q (%s) returned HTTP %d for POST /v1/pull: %s", target, endpoint, resp.StatusCode, errResp["error"]))
		} else {
			emitPullError(jsonMode, fmt.Sprintf("pull failed — target %q (%s) returned HTTP %d for POST /v1/pull", target, endpoint, resp.StatusCode))
		}
		if !jsonMode {
			if hint := pullStatusHint(resp.StatusCode); hint != "" {
				fmt.Fprintf(os.Stderr, "  hint: %s\n", hint)
			}
		}
		return 1
	}

	var pullResp daemon.PullResponse
	if err := json.Unmarshal(respBody, &pullResp); err != nil {
		emitPullError(jsonMode, fmt.Sprintf("invalid response from target: %v", err))
		return 1
	}

	if flags["json"] == "true" {
		output := struct {
			daemon.PullResponse
			Placement *daemon.PlacementResult `json:"placement,omitempty"`
		}{
			PullResponse: pullResp,
			Placement:    placementResult,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(output)
	} else {
		fmt.Printf("  job_id    %s\n", pullResp.JobID)
		fmt.Printf("  status    %s\n", pullResp.Status)
		fmt.Printf("  target    %s\n", pullResp.Target)
		if placementResult != nil {
			fmt.Printf("  placed    %s (%s)\n", placementResult.Target, placementResult.Reason)
		}
	}

	return 0
}

// resolveTargetNode looks up a node's address and port from the mesh state.
func resolveTargetNode(name string) (string, int, error) {
	nodes, err := daemon.LoadMeshState()
	if err != nil {
		return "", 0, fmt.Errorf("cannot load mesh state: %v", err)
	}
	if nodes == nil {
		return "", 0, fmt.Errorf("no mesh state — is this node part of a mesh?")
	}
	for _, n := range nodes {
		if strings.EqualFold(n.Name, name) {
			if n.Address == "" {
				return "", 0, fmt.Errorf("node %q has no address", name)
			}
			port := n.Port
			if port == 0 {
				port = meshconfig.DefaultPort
			}
			return n.Address, port, nil
		}
	}
	return "", 0, fmt.Errorf("node %q not found in mesh (known nodes: %s)", name, knownNodeNames(nodes))
}

func knownNodeNames(nodes []daemon.MeshNode) string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return strings.Join(names, ", ")
}

// loadMeshKey loads the mesh key from disk (best-effort).
func loadMeshKey() string {
	data, err := os.ReadFile(meshconfig.MeshKeyPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// pullConnectionHint returns a human-readable hint for connection errors.
func pullConnectionHint(err error, target string) string {
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "connection refused"):
		return fmt.Sprintf("check 'model-shelf service status' on %s or run 'model-shelf nodes' to verify status", target)
	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded"):
		return fmt.Sprintf("target %s may be unreachable or overloaded", target)
	case strings.Contains(errStr, "no such host"):
		return fmt.Sprintf("cannot resolve hostname for %s — check DNS or /etc/hosts", target)
	case strings.Contains(errStr, "no route"):
		return fmt.Sprintf("no route to %s — check network connectivity and firewall rules", target)
	default:
		return ""
	}
}

// pullStatusHint returns a hint based on HTTP status code from the target.
func pullStatusHint(statusCode int) string {
	switch statusCode {
	case http.StatusNotFound:
		return "ensure the target node is running model-shelf v0.14+ with the pull endpoint"
	case http.StatusUnauthorized:
		return "mesh key mismatch — re-join with the correct key"
	case http.StatusForbidden:
		return "mesh key mismatch — re-join with the correct key"
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return "target node's daemon may be starting up or overloaded"
	default:
		return ""
	}
}

// emitPullError outputs an error as JSON or plain text based on the mode.
func emitPullError(jsonMode bool, msg string) {
	if jsonMode {
		enc := json.NewEncoder(os.Stdout)
		enc.Encode(map[string]string{"error": msg})
	} else {
		fmt.Fprintf(os.Stderr, "error: %s\n", msg)
	}
}

// autoSelectTarget uses smart placement to pick the best Executor for a model pull.
func autoSelectTarget(repoID, format, quant string) (*daemon.PlacementResult, error) {
	// Load mesh config to talk to daemon.
	if !meshconfig.Exists() {
		return nil, fmt.Errorf("no mesh config — run `model-shelf init` first")
	}
	cfg, err := meshconfig.Load()
	if err != nil {
		return nil, fmt.Errorf("loading mesh config: %v", err)
	}

	// Get mesh nodes from the local daemon.
	nodes, err := fetchNodes(cfg)
	if err != nil {
		return nil, fmt.Errorf("querying mesh nodes: %v", err)
	}

	// Get mesh jobs to determine which nodes are "serving".
	jobs, err := fetchJobs(cfg)
	if err != nil {
		// Log warning but proceed — better to place without "not serving" info than to fail.
		fmt.Fprintf(os.Stderr, "warning: could not fetch mesh jobs for smart placement: %v\n", err)
	}

	activeJobCountByNode := make(map[string]int)
	for _, j := range jobs {
		if j.Status == daemon.JobQueued || j.Status == daemon.JobDownloading || j.Status == daemon.JobTransferring || j.Status == daemon.JobEvicting {
			activeJobCountByNode[j.Target]++
		}
	}

	// Skip VRAM estimation when all executor nodes are CPU-only — the HF API call
	// is unnecessary and would fail spuriously when the quant doesn't exist on HF.
	var estimatedVRAMGB float64
	if !daemon.AllExecutorsCPUOnly(nodes) {
		estimatedVRAMGB, err = daemon.EstimateModelVRAM(repoID, format, quant)
		if err != nil {
			if _, ok := err.(*daemon.QuantNotFoundError); ok {
				return nil, err // friendly message already; no wrapper needed
			}
			return nil, fmt.Errorf("estimating model VRAM: %v", err)
		}
	}

	// Build per-node inventory map.
	inventoryByNode := make(map[string][]daemon.InventoryEntry)
	for _, node := range nodes {
		if node.Status == daemon.StatusOffline {
			continue
		}
		if !daemon.HasRole(node.Roles, "executor") {
			continue
		}
		entries, _ := fetchNodeInventory(node, cfg)
		inventoryByNode[node.Name] = entries
	}

	return daemon.SelectExecutor(nodes, estimatedVRAMGB, repoID, format, quant, inventoryByNode, activeJobCountByNode)
}

// fetchJobs queries the local daemon for the list of mesh-wide jobs.
func fetchJobs(cfg *meshconfig.Config) ([]daemon.Job, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/jobs?mesh=true", cfg.Port)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if cfg.MeshKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.MeshKey)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon returned HTTP %d", resp.StatusCode)
	}

	var jobs []daemon.Job
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}
