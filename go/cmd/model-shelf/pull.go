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

	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf pull <repo_id> [--target <node>] [--quant Q] [--format F] [--json]")
		fmt.Println()
		fmt.Println("Pull a model from Hugging Face to a target node.")
		fmt.Println("If --target is omitted, auto-selects the best Executor based on VRAM capacity.")
		fmt.Println("Fire-and-forget: returns a job ID immediately.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --target <node>    Target node name (auto-selects if omitted)")
		fmt.Println("  --quant <Q>        Quantization level (required for GGUF)")
		fmt.Println("  --format <F>       Force format: gguf, mlx, safetensors")
		fmt.Println("  --json             Emit JSON output")
		return 0
	}

	if len(positional) < 1 {
		fmt.Fprintf(os.Stderr, "usage: model-shelf pull <repo_id> [--target <node>] [--quant Q] [--format F] [--json]\n")
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
		fmt.Fprintf(os.Stderr, "error: unsupported format: %q\n", format)
		return 1
	}

	quant := flags["quant"]
	if format == "gguf" && quant == "" {
		fmt.Fprintf(os.Stderr, "error: --quant is required for gguf format\n")
		return 1
	}

	// If no target specified, use smart placement to auto-select an Executor.
	var placementResult *daemon.PlacementResult
	if target == "" {
		result, err := autoSelectTarget(repoID, format, quant)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		target = result.Target
		placementResult = result
	}

	// Look up the target node's address from mesh state.
	addr, port, err := resolveTargetNode(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Load mesh key for auth.
	meshKey := loadMeshKey()

	// Send POST /v1/pull to the target.
	pullReq := daemon.PullRequest{
		RepoID: repoID,
		Format: format,
		Quant:  quant,
	}
	body, err := json.Marshal(pullReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	url := fmt.Sprintf("http://%s/v1/pull", net.JoinHostPort(addr, fmt.Sprintf("%d", port)))
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if meshKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+meshKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		endpoint := net.JoinHostPort(addr, fmt.Sprintf("%d", port))
		hint := pullConnectionHint(err, target)
		fmt.Fprintf(os.Stderr, "error: pull failed — cannot reach target %q (%s) for POST /v1/pull\n", target, endpoint)
		fmt.Fprintf(os.Stderr, "  %v\n", err)
		if hint != "" {
			fmt.Fprintf(os.Stderr, "  hint: %s\n", hint)
		}
		return 1
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading response: %v\n", err)
		return 1
	}

	if resp.StatusCode != http.StatusAccepted {
		endpoint := net.JoinHostPort(addr, fmt.Sprintf("%d", port))
		var errResp map[string]string
		if json.Unmarshal(respBody, &errResp) == nil && errResp["error"] != "" {
			fmt.Fprintf(os.Stderr, "error: pull failed — target %q (%s) returned HTTP %d for POST /v1/pull: %s\n", target, endpoint, resp.StatusCode, errResp["error"])
		} else {
			fmt.Fprintf(os.Stderr, "error: pull failed — target %q (%s) returned HTTP %d for POST /v1/pull\n", target, endpoint, resp.StatusCode)
		}
		if hint := pullStatusHint(resp.StatusCode); hint != "" {
			fmt.Fprintf(os.Stderr, "  hint: %s\n", hint)
		}
		return 1
	}

	var pullResp daemon.PullResponse
	if err := json.Unmarshal(respBody, &pullResp); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid response from target: %v\n", err)
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
		return fmt.Sprintf("is the daemon running on %s? check 'model-shelf service status' on %s", target, target)
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

	// Estimate VRAM requirement from HF API.
	estimatedVRAMGB, err := daemon.EstimateModelVRAM(repoID, format, quant)
	if err != nil {
		return nil, fmt.Errorf("estimating model VRAM: %v", err)
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

	return daemon.SelectExecutor(nodes, estimatedVRAMGB, repoID, format, quant, inventoryByNode)
}
