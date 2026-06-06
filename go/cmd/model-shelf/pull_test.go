package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alexziskind1/model-shelf/internal/daemon"
)

func TestCmdPull_Success(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Start a fake target daemon that responds to POST /v1/pull.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/pull" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var req daemon.PullRequest
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(daemon.PullResponse{
			JobID:  "abc123",
			Status: daemon.JobQueued,
			Target: "target-node",
		})
	}))
	defer server.Close()

	// Parse the server URL (handles IPv6 correctly).
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())

	// Write mesh state with the target node pointing at our test server.
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	nodes := []daemon.MeshNode{
		{Name: "target-node", Address: host, Port: port, Roles: []string{"store"}},
	}
	data, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), data, 0o644)

	// Run the pull command.
	code := cmdPull([]string{"mlx-community/test-model-mlx", "--target", "target-node", "--json"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
}

func TestCmdPull_MissingTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	code := cmdPull([]string{"some/model"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
}

func TestCmdPull_MissingRepoID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	code := cmdPull([]string{"--target", "some-node"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
}

func TestCmdPull_GGUFRequiresQuant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	code := cmdPull([]string{"Qwen/Qwen3-14B-GGUF", "--target", "node1", "--format", "gguf"})
	if code != 1 {
		t.Fatalf("expected exit code 1 for gguf without quant, got %d", code)
	}
}

func TestCmdPull_NodeNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Write empty mesh state.
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), []byte("[]"), 0o644)

	code := cmdPull([]string{"test/model", "--target", "nonexistent"})
	if code != 1 {
		t.Fatalf("expected exit code 1 for unknown node, got %d", code)
	}
}

func TestCmdPull_Help(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	code := cmdPull([]string{"--help"})
	if code != 0 {
		t.Fatalf("expected exit code 0 for help, got %d", code)
	}
}

func TestCmdPull_ErrorMessage_404(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Start a fake target that returns 404.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())

	// Write mesh state.
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	nodes := []daemon.MeshNode{
		{Name: "old-node", Address: host, Port: port, Roles: []string{"store"}},
	}
	data, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), data, 0o644)

	// Capture stderr.
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := cmdPull([]string{"mlx-community/test-model-mlx", "--target", "old-node"})

	w.Close()
	os.Stderr = oldStderr
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	errOutput := string(out)
	if !strings.Contains(errOutput, "old-node") {
		t.Errorf("error should mention target name, got: %s", errOutput)
	}
	if !strings.Contains(errOutput, "hint:") {
		t.Errorf("error should include a hint, got: %s", errOutput)
	}
}

func TestCmdPull_ErrorMessage_401(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())

	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	nodes := []daemon.MeshNode{
		{Name: "secure-node", Address: host, Port: port, Roles: []string{"store"}},
	}
	data, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), data, 0o644)

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := cmdPull([]string{"mlx-community/test-model-mlx", "--target", "secure-node"})

	w.Close()
	os.Stderr = oldStderr
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	errOutput := string(out)
	if !strings.Contains(errOutput, "secure-node") {
		t.Errorf("error should mention target name, got: %s", errOutput)
	}
	if !strings.Contains(errOutput, "mesh key") {
		t.Errorf("error should hint about mesh key, got: %s", errOutput)
	}
}
