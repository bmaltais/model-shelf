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
		fmt.Fprintf(os.Stderr, "error: cannot reach target node %q: %v\n", target, err)
		return 1
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading response: %v\n", err)
		return 1
	}

	if resp.StatusCode != http.StatusAccepted {
		var errResp map[string]string
		if json.Unmarshal(respBody, &errResp) == nil && errResp["error"] != "" {
			fmt.Fprintf(os.Stderr, "error: %s\n", errResp["error"])
		} else {
			fmt.Fprintf(os.Stderr, "error: target returned HTTP %d\n", resp.StatusCode)
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
