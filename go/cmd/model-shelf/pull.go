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
		fmt.Println("Usage: model-shelf pull <repo_id> --target <node> [--quant Q] [--format F] [--json]")
		fmt.Println()
		fmt.Println("Pull a model from Hugging Face to a target node.")
		fmt.Println("Fire-and-forget: returns a job ID immediately.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --target <node>    Target node name (required)")
		fmt.Println("  --quant <Q>        Quantization level (required for GGUF)")
		fmt.Println("  --format <F>       Force format: gguf, mlx, safetensors")
		fmt.Println("  --json             Emit JSON output")
		return 0
	}

	if len(positional) < 1 {
		fmt.Fprintf(os.Stderr, "usage: model-shelf pull <repo_id> --target <node> [--quant Q] [--format F] [--json]\n")
		return 1
	}
	repoID := positional[0]
	target := flags["target"]
	if target == "" {
		fmt.Fprintf(os.Stderr, "error: --target is required\n")
		return 1
	}

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
		hint := pullConnectionHint(err, target, addr, port)
		fmt.Fprintf(os.Stderr, "error: pull failed — cannot reach target %q (%s)\n", target, net.JoinHostPort(addr, fmt.Sprintf("%d", port)))
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
			fmt.Fprintf(os.Stderr, "error: pull failed — target %q (%s) returned: %s\n", target, endpoint, errResp["error"])
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
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(pullResp)
	} else {
		fmt.Printf("  job_id    %s\n", pullResp.JobID)
		fmt.Printf("  status    %s\n", pullResp.Status)
		fmt.Printf("  target    %s\n", pullResp.Target)
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
func pullConnectionHint(err error, target, addr string, port int) string {
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "connection refused"):
		return fmt.Sprintf("is the daemon running on %s? check 'model-shelf service status' on %s", target, target)
	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded"):
		return fmt.Sprintf("target %s may be unreachable or overloaded", target)
	case strings.Contains(errStr, "no such host") || strings.Contains(errStr, "no route"):
		return fmt.Sprintf("cannot resolve hostname for %s — check network connectivity", target)
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
