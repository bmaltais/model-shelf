package main

import (
	"encoding/json"
	"io"
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
	// Build a lookup by name for assertions.
	nodeByName := map[string]daemon.MeshNode{}
	for _, n := range nodes {
		nodeByName[n.Name] = n
	}
	// Verify self node.
	self, ok := nodeByName["joining-node"]
	if !ok {
		t.Fatalf("mesh.json missing self (joining-node): %+v", nodes)
	}
	if self.Port != 8844 || self.Status != daemon.StatusOnline {
		t.Errorf("self node: unexpected port=%d status=%s", self.Port, self.Status)
	}
	if len(self.Roles) != 1 || self.Roles[0] != "store" {
		t.Errorf("self node: unexpected roles=%v", self.Roles)
	}
	// Verify peer node.
	peer, ok := nodeByName["controller"]
	if !ok {
		t.Fatalf("mesh.json missing peer (controller): %+v", nodes)
	}
	if peer.Address != "10.0.0.1" || peer.Port != 8844 {
		t.Errorf("peer node: unexpected address=%s port=%d", peer.Address, peer.Port)
	}
	if peer.Status != daemon.StatusOnline {
		t.Errorf("peer node: unexpected status=%s", peer.Status)
	}
	if len(peer.Roles) != 1 || peer.Roles[0] != "controller" {
		t.Errorf("peer node: unexpected roles=%v", peer.Roles)
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

func TestCmdJoin_MeshKeyFromEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MODEL_SHELF_MESH_KEY", "env-key-456")

	// Set up local mesh config.
	cfg := &meshconfig.Config{
		Name:      "joining-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	// Start a fake peer server that verifies the key from env.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer env-key-456" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		if r.URL.Path == "/v1/join" && r.Method == http.MethodPost {
			resp := daemon.JoinResponse{
				OK:    true,
				Nodes: []daemon.NodeInfo{},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	peerAddr := strings.TrimPrefix(server.URL, "http://")
	code := cmdJoin([]string{peerAddr})
	if code != 0 {
		t.Fatalf("expected exit 0 with env key, got %d", code)
	}

	// Verify mesh key was stored.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("mesh.key not written: %v", err)
	}
	if strings.TrimSpace(string(keyData)) != "env-key-456" {
		t.Errorf("unexpected mesh key: %q", string(keyData))
	}
}

func TestCmdJoin_NonInteractiveNoKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MODEL_SHELF_MESH_KEY", "") // ensure env is unset

	// Set up local mesh config.
	cfg := &meshconfig.Config{
		Name:      "joining-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	// Replace stdin with a pipe (non-interactive).
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Close() // close write end immediately — simulates non-interactive
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	// Capture stderr.
	oldStderr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	code := cmdJoin([]string{"somehost:8844"})

	wErr.Close()
	os.Stderr = oldStderr
	errOut, _ := io.ReadAll(rErr)

	if code != 1 {
		t.Fatalf("expected exit 1 in non-interactive mode without key, got %d", code)
	}
	if !strings.Contains(string(errOut), "mesh key is required") {
		t.Errorf("expected 'mesh key is required' error, got: %q", string(errOut))
	}
	if !strings.Contains(string(errOut), "MODEL_SHELF_MESH_KEY") {
		t.Errorf("expected hint about MODEL_SHELF_MESH_KEY, got: %q", string(errOut))
	}
}

func TestCmdJoin_DiskMetricsPropagated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Set up local mesh config.
	cfg := &meshconfig.Config{
		Name:      "joining-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	// Start a fake peer server that returns disk metrics in the join response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/v1/join" && r.Method == http.MethodPost {
			resp := daemon.JoinResponse{
				OK: true,
				Nodes: []daemon.NodeInfo{
					{
						Name:        "controller",
						Address:     "10.0.0.1",
						Port:        8844,
						Roles:       []string{"controller", "store"},
						DiskFreeGB:  512.5,
						DiskTotalGB: 1024.0,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	peerAddr := strings.TrimPrefix(server.URL, "http://")

	code := cmdJoin([]string{peerAddr, "--key", "test-key"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify disk metrics were persisted to mesh.json.
	statePath := filepath.Join(home, ".model-shelf", "state", "mesh.json")
	stateData, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("mesh.json not written: %v", err)
	}
	var nodes []daemon.MeshNode
	if err := json.Unmarshal(stateData, &nodes); err != nil {
		t.Fatalf("invalid mesh.json: %v", err)
	}

	// Find the controller node and verify disk metrics.
	var found bool
	for _, n := range nodes {
		if n.Name == "controller" {
			found = true
			if n.DiskFreeGB != 512.5 {
				t.Errorf("DiskFreeGB = %f, want 512.5", n.DiskFreeGB)
			}
			if n.DiskTotalGB != 1024.0 {
				t.Errorf("DiskTotalGB = %f, want 1024.0", n.DiskTotalGB)
			}
			break
		}
	}
	if !found {
		t.Fatalf("controller node not found in mesh.json: %+v", nodes)
	}
}
