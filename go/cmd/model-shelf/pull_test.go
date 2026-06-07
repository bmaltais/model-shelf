package main

import (
	"encoding/json"
	"fmt"
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
	"github.com/alexziskind1/model-shelf/internal/resolver"
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

func TestCmdPull_JSONError_MissingRepoID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Capture stdout for JSON error output.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdPull([]string{"--json"})

	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	var errResp map[string]string
	if err := json.Unmarshal(out, &errResp); err != nil {
		t.Fatalf("expected JSON error output, got: %s (parse error: %v)", out, err)
	}
	if errResp["error"] == "" {
		t.Fatalf("expected non-empty error field in JSON, got: %s", out)
	}
}

func TestCmdPull_JSONError_GGUFNoQuant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdPull([]string{"Qwen/Qwen3-14B-GGUF", "--target", "node1", "--format", "gguf", "--json"})

	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	var errResp map[string]string
	if err := json.Unmarshal(out, &errResp); err != nil {
		t.Fatalf("expected JSON error output, got: %s (parse error: %v)", out, err)
	}
	if !strings.Contains(errResp["error"], "quant") {
		t.Fatalf("expected error about quant, got: %s", errResp["error"])
	}
}

func TestCmdPull_JSONError_NodeNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Write empty mesh state.
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), []byte("[]"), 0o644)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdPull([]string{"test/model", "--target", "nonexistent", "--json"})

	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	var errResp map[string]string
	if err := json.Unmarshal(out, &errResp); err != nil {
		t.Fatalf("expected JSON error output, got: %s (parse error: %v)", out, err)
	}
	if !strings.Contains(errResp["error"], "nonexistent") {
		t.Fatalf("expected error mentioning node name, got: %s", errResp["error"])
	}
}

func TestCmdPull_JSONError_HTTPError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Start a fake target that returns 401.
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
		{Name: "auth-node", Address: host, Port: port, Roles: []string{"store"}},
	}
	data, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), data, 0o644)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdPull([]string{"mlx-community/test-model-mlx", "--target", "auth-node", "--json"})

	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	var errResp map[string]string
	if err := json.Unmarshal(out, &errResp); err != nil {
		t.Fatalf("expected JSON error output, got: %s (parse error: %v)", out, err)
	}
	if errResp["error"] == "" {
		t.Fatalf("expected non-empty error in JSON output, got: %s", out)
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

func TestCmdPull_ErrorMessage_ConnectionRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Point at a port where nothing is listening.
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	nodes := []daemon.MeshNode{
		{Name: "dead-node", Address: "127.0.0.1", Port: 19999, Roles: []string{"store"}},
	}
	data, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), data, 0o644)

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := cmdPull([]string{"mlx-community/test-model-mlx", "--target", "dead-node"})

	w.Close()
	os.Stderr = oldStderr
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	errOutput := string(out)
	if !strings.Contains(errOutput, "dead-node") {
		t.Errorf("error should mention target name, got: %s", errOutput)
	}
	if !strings.Contains(errOutput, "POST /v1/pull") {
		t.Errorf("error should mention endpoint, got: %s", errOutput)
	}
	if !strings.Contains(errOutput, "hint:") {
		t.Errorf("error should include a hint for connection refused, got: %s", errOutput)
	}
	if !strings.Contains(errOutput, "daemon running") {
		t.Errorf("hint should suggest checking if daemon is running, got: %s", errOutput)
	}
}

func TestPullConnectionHint(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		wantSub string
	}{
		{"connection refused", "dial tcp 127.0.0.1:8844: connection refused", "daemon running"},
		{"timeout", "context deadline exceeded", "unreachable or overloaded"},
		{"no such host", "dial tcp: lookup badhost: no such host", "resolve hostname"},
		{"no route", "dial tcp 10.0.0.99:8844: connect: no route to host", "no route"},
		{"unknown error", "something else went wrong", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("%s", tc.errMsg)
			hint := pullConnectionHint(err, "target-node")
			if tc.wantSub == "" {
				if hint != "" {
					t.Errorf("expected empty hint, got %q", hint)
				}
			} else {
				if !strings.Contains(hint, tc.wantSub) {
					t.Errorf("expected hint to contain %q, got %q", tc.wantSub, hint)
				}
			}
		})
	}
}

func TestPullStatusHint(t *testing.T) {
	tests := []struct {
		code    int
		wantSub string
	}{
		{404, "v0.14+"},
		{401, "mesh key"},
		{403, "mesh key"},
		{503, "starting up"},
		{200, ""},
	}
	for _, tc := range tests {
		hint := pullStatusHint(tc.code)
		if tc.wantSub == "" {
			if hint != "" {
				t.Errorf("code %d: expected empty hint, got %q", tc.code, hint)
			}
		} else {
			if !strings.Contains(hint, tc.wantSub) {
				t.Errorf("code %d: expected hint containing %q, got %q", tc.code, tc.wantSub, hint)
			}
		}
	}
}

// TestCmdPull_AlreadyPresentLocally verifies that when the model already exists
// on the local shelf and --force is not set, cmdPull returns 0 with already_present
// without calling the placement engine.
func TestCmdPull_AlreadyPresentLocally(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	// Create the GGUF file at the resolver-expected path.
	modelPath, err := resolver.ShelfPathGGUF(shelfRoot, "unsloth/Qwen3-0.6B-GGUF", "Q4_K_M")
	if err != nil {
		t.Fatalf("ShelfPathGGUF: %v", err)
	}
	os.MkdirAll(filepath.Dir(modelPath), 0o755)
	os.WriteFile(modelPath, []byte("fake gguf content"), 0o644)

	// Write mesh config; placement engine should never be called.
	meshCfgDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(meshCfgDir, 0o755)
	meshCfgContent := fmt.Sprintf("name = \"local-node\"\nport = 19998\nroles = [\"executor\"]\nshelf_root = %q\n", shelfRoot)
	os.WriteFile(filepath.Join(meshCfgDir, "config.toml"), []byte(meshCfgContent), 0o644)

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdPull([]string{"unsloth/Qwen3-0.6B-GGUF", "--quant", "Q4_K_M"})

	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit code 0 (already_present locally), got %d", code)
	}
	if !strings.Contains(string(out), "already_present") {
		t.Errorf("expected 'already_present' in output, got: %s", out)
	}
	if !strings.Contains(string(out), "local-node") {
		t.Errorf("expected local node name in output, got: %s", out)
	}
}

// TestCmdPull_AlreadyPresentLocally_JSON verifies the JSON output shape for the
// already-present-locally case.
func TestCmdPull_AlreadyPresentLocally_JSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	modelPath, err := resolver.ShelfPathGGUF(shelfRoot, "unsloth/Qwen3-0.6B-GGUF", "Q4_K_M")
	if err != nil {
		t.Fatalf("ShelfPathGGUF: %v", err)
	}
	os.MkdirAll(filepath.Dir(modelPath), 0o755)
	os.WriteFile(modelPath, []byte("fake gguf content"), 0o644)

	meshCfgDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(meshCfgDir, 0o755)
	meshCfgContent := fmt.Sprintf("name = \"local-node\"\nport = 19997\nroles = [\"executor\"]\nshelf_root = %q\n", shelfRoot)
	os.WriteFile(filepath.Join(meshCfgDir, "config.toml"), []byte(meshCfgContent), 0o644)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdPull([]string{"unsloth/Qwen3-0.6B-GGUF", "--quant", "Q4_K_M", "--json"})

	w.Close()
	os.Stdout = oldStdout
	outBytes, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit code 0 (already_present locally), got %d", code)
	}
	var resp daemon.PullResponse
	if err := json.Unmarshal(outBytes, &resp); err != nil {
		t.Fatalf("expected JSON output, got: %s (parse error: %v)", outBytes, err)
	}
	if resp.Status != daemon.JobAlreadyPresent {
		t.Errorf("expected status %q, got %q", daemon.JobAlreadyPresent, resp.Status)
	}
	if resp.Target != "local-node" {
		t.Errorf("expected target 'local-node', got %q", resp.Target)
	}
}

// TestCmdPull_ForceBypassesLocalCheck verifies that --force skips the local shelf
// check and proceeds to placement (which fails here because the daemon is unreachable).
func TestCmdPull_ForceBypassesLocalCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	modelPath, err := resolver.ShelfPathGGUF(shelfRoot, "unsloth/Qwen3-0.6B-GGUF", "Q4_K_M")
	if err != nil {
		t.Fatalf("ShelfPathGGUF: %v", err)
	}
	os.MkdirAll(filepath.Dir(modelPath), 0o755)
	os.WriteFile(modelPath, []byte("fake gguf content"), 0o644)

	meshCfgDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(meshCfgDir, 0o755)
	meshCfgContent := fmt.Sprintf("name = \"local-node\"\nport = 19996\nroles = [\"executor\"]\nshelf_root = %q\n", shelfRoot)
	os.WriteFile(filepath.Join(meshCfgDir, "config.toml"), []byte(meshCfgContent), 0o644)

	code := cmdPull([]string{"unsloth/Qwen3-0.6B-GGUF", "--quant", "Q4_K_M", "--force"})
	// --force bypasses the local check and calls autoSelectTarget, which fails
	// because the daemon is not running.
	if code == 0 {
		t.Fatalf("expected non-zero exit code when --force bypasses local check, got 0")
	}
}
