package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func cmdJoin(args []string) int {
	positional, flags := parseFlags(args)
	if len(positional) < 1 {
		fmt.Fprintf(os.Stderr, "usage: model-shelf join <peer> [--key <mesh-key>]\n")
		return 1
	}

	peer := positional[0]
	peerAddr := normalizePeerAddr(peer)

	// Get mesh key from --key flag or prompt.
	meshKey := flags["key"]
	if meshKey == "" {
		meshKey = promptMeshKey()
		if meshKey == "" {
			fmt.Fprintf(os.Stderr, "error: mesh key is required\n")
			return 1
		}
	}

	// Load local mesh config to get our node info.
	if !meshconfig.Exists() {
		fmt.Fprintf(os.Stderr, "error: mesh not configured. Run 'model-shelf init' first.\n")
		return 1
	}
	cfg, err := meshconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Build the join request.
	joinReq := daemon.JoinRequest{
		Name:    cfg.Name,
		Address: meshconfig.GetHostname(),
		Port:    cfg.Port,
		Roles:   cfg.Roles,
	}
	body, _ := json.Marshal(joinReq)

	// Send join request to peer.
	url := fmt.Sprintf("http://%s/v1/join", peerAddr)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+meshKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: could not reach peer %s: %v\n", peerAddr, err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		fmt.Fprintf(os.Stderr, "error: mesh key rejected by peer\n")
		return 1
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: peer returned %d: %s\n", resp.StatusCode, string(respBody))
		return 1
	}

	var joinResp daemon.JoinResponse
	if err := json.NewDecoder(resp.Body).Decode(&joinResp); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid response from peer: %v\n", err)
		return 1
	}

	// Store mesh key locally.
	if err := meshconfig.WriteMeshKey(meshKey); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to store mesh key: %v\n", err)
		return 1
	}

	// Store peer address as seed in config.
	if !containsSeed(cfg.Seeds, peerAddr) {
		cfg.Seeds = append(cfg.Seeds, peerAddr)
	}
	if err := meshconfig.Write(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to update config: %v\n", err)
		return 1
	}

	fmt.Printf("model-shelf: joined mesh via %s\n", peerAddr)
	fmt.Printf("model-shelf: stored mesh key at %s\n", meshconfig.MeshKeyPath())
	if len(joinResp.Nodes) > 0 {
		fmt.Printf("model-shelf: received %d node(s) from mesh:\n", len(joinResp.Nodes))
		for _, n := range joinResp.Nodes {
			fmt.Printf("  - %s (roles: %s, port: %d)\n", n.Name, strings.Join(n.Roles, ","), n.Port)
		}
	}

	return 0
}

// normalizePeerAddr adds the default port if not specified.
func normalizePeerAddr(peer string) string {
	if strings.Contains(peer, ":") {
		return peer
	}
	return fmt.Sprintf("%s:%d", peer, meshconfig.DefaultPort)
}

// promptMeshKey reads the mesh key from stdin.
func promptMeshKey() string {
	fmt.Fprint(os.Stderr, "Mesh key: ")
	var key string
	fmt.Scanln(&key)
	return strings.TrimSpace(key)
}

func containsSeed(seeds []string, addr string) bool {
	for _, s := range seeds {
		if s == addr {
			return true
		}
	}
	return false
}
