package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestCmdJoin_Success(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Set up local mesh config (as if init was already run).
	cfg := &meshconfig.Config{
		Name:      "joining-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	// Start a fake peer server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key-123" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}

		if r.URL.Path == "/v1/join" && r.Method == http.MethodPost {
			var req daemon.JoinRequest
			json.NewDecoder(r.Body).Decode(&req)
			resp := daemon.JoinResponse{
				OK: true,
				Nodes: []daemon.NodeInfo{
					{Name: "controller", Address: "10.0.0.1", Port: 8844, Roles: []string{"controller"}},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	// Extract host:port from the test server URL.
	peerAddr := strings.TrimPrefix(server.URL, "http://")

	code := cmdJoin([]string{peerAddr, "--key", "test-key-123"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify mesh key was stored.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("mesh.key not written: %v", err)
	}
	if strings.TrimSpace(string(keyData)) != "test-key-123" {
		t.Errorf("unexpected mesh key: %q", string(keyData))
	}

	// Verify seed was stored in config.
	updatedCfg, err := meshconfig.LoadFrom(meshconfig.ConfigPath())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if len(updatedCfg.Seeds) != 1 || updatedCfg.Seeds[0] != peerAddr {
		t.Errorf("expected seed %q, got %v", peerAddr, updatedCfg.Seeds)
	}

	// Verify mesh state was persisted.
	statePath := filepath.Join(home, ".model-shelf", "state", "mesh.json")
	stateData, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("mesh.json not written: %v", err)
	}
	var nodes []daemon.MeshNode
	if err := json.Unmarshal(stateData, &nodes); err != nil {
		t.Fatalf("invalid mesh.json: %v", err)
	}
	// Should contain self + the controller node from the peer response.
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes in mesh.json, got %d: %+v", len(nodes), nodes)
	}
	names := map[string]bool{}
	for _, n := range nodes {
		names[n.Name] = true
	}
	if !names["joining-node"] {
		t.Errorf("mesh.json missing self (joining-node): %+v", nodes)
	}
	if !names["controller"] {
		t.Errorf("mesh.json missing peer (controller): %+v", nodes)
	}
}

func TestCmdJoin_RequiresPeer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdJoin([]string{})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestCmdJoin_RequiresInit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdJoin([]string{"somehost", "--key", "abc"})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestCmdJoin_RejectedKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "joining-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "unauthorized"}`))
	}))
	defer server.Close()

	peerAddr := strings.TrimPrefix(server.URL, "http://")
	code := cmdJoin([]string{peerAddr, "--key", "wrong-key"})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestNormalizePeerAddr(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"mini1", "mini1:8844"},
		{"mini1:9900", "mini1:9900"},
		{"192.168.1.1", "192.168.1.1:8844"},
		{"192.168.1.1:7700", "192.168.1.1:7700"},
		{"[::1]", "[::1]:8844"},
		{"[::1]:9900", "[::1]:9900"},
	}
	for _, tt := range tests {
		got := normalizePeerAddr(tt.input)
		if got != tt.want {
			t.Errorf("normalizePeerAddr(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
