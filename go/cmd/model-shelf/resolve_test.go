package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/resolver"
)

func TestCmdResolve_QuantWarningForMLX(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create minimal shelf and config.
	shelfRoot := filepath.Join(home, "shelf")
	os.MkdirAll(filepath.Join(shelfRoot, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Capture stderr.
	oldErr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Run resolve with --quant on an MLX model (will be "missing" but we
	// only care about the warning).
	_ = cmdResolve([]string{"mlx-community/Qwen3-0.6B-4bit", "--quant", "Q4_K_M", "--no-download"})

	w.Close()
	os.Stderr = oldErr
	out, _ := io.ReadAll(r)

	if !strings.Contains(string(out), "warning: --quant is only used for gguf format") {
		t.Errorf("expected quant warning on stderr, got: %q", string(out))
	}
}

func TestCmdResolve_MalformedRepoID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create minimal shelf and config.
	shelfRoot := filepath.Join(home, "shelf")
	os.MkdirAll(filepath.Join(shelfRoot, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Capture stderr.
	oldErr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := cmdResolve([]string{"too/many/slashes", "--no-download"})

	w.Close()
	os.Stderr = oldErr
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(string(out), "publisher/repo") {
		t.Errorf("expected validation error mentioning 'publisher/repo', got: %q", string(out))
	}
}

func TestCmdResolve_ExplicitConfigNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Capture stderr.
	oldErr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	missingCfg := filepath.Join(t.TempDir(), "nonexistent", "config.toml")
	code := cmdResolve([]string{"unsloth/Qwen3-0.6B-GGUF", "--quant", "Q4_K_M", "--no-download", "--config", missingCfg})

	w.Close()
	os.Stderr = oldErr
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(string(out), "config file not found") {
		t.Errorf("expected 'config file not found' error, got: %q", string(out))
	}
}

func TestCmdResolve_QuantNoWarningForGGUF(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	os.MkdirAll(filepath.Join(shelfRoot, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Capture stderr.
	oldErr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	_ = cmdResolve([]string{"unsloth/Qwen3-0.6B-GGUF", "--quant", "Q4_K_M", "--no-download"})

	w.Close()
	os.Stderr = oldErr
	out, _ := io.ReadAll(r)

	if strings.Contains(string(out), "warning: --quant") {
		t.Errorf("unexpected quant warning for GGUF format, got: %q", string(out))
	}
}

func TestCmdResolve_MeshPeersQueried(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create minimal shelf and resolver config.
	shelfRoot := filepath.Join(home, "shelf")
	os.MkdirAll(filepath.Join(shelfRoot, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Set up a fake peer inventory endpoint that reports having the model.
	peerInventory := []daemon.InventoryEntry{
		{RepoID: "bartowski/Llama-3.2-1B-Instruct-GGUF", Format: "gguf", Quant: "Q4_K_M", SizeBytes: 1000000},
	}
	peerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/nodes":
			// Return a node list with the peer.
			json.NewEncoder(w).Encode([]daemon.MeshNode{
				{Name: "self-node", Address: "127.0.0.1", Port: 8844, Status: daemon.StatusOnline},
				{Name: "mini1", Address: "PEER_ADDR", Port: 0, Status: daemon.StatusOnline},
			})
		case "/v1/inventory":
			json.NewEncoder(w).Encode(peerInventory)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	// Start a fake daemon (nodes endpoint) on a random port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	daemonPort := ln.Addr().(*net.TCPAddr).Port

	// Start a fake peer server on another port.
	peerLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for peer: %v", err)
	}
	peerPort := peerLn.Addr().(*net.TCPAddr).Port

	// Replace PEER_ADDR in node list dynamically.
	nodesHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/nodes":
			nodes := []daemon.MeshNode{
				{Name: "self-node", Address: "127.0.0.1", Port: daemonPort, Status: daemon.StatusOnline},
				{Name: "mini1", Address: "127.0.0.1", Port: peerPort, Status: daemon.StatusOnline},
			}
			json.NewEncoder(w).Encode(nodes)
		case "/v1/inventory":
			json.NewEncoder(w).Encode(peerInventory)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	daemonServer := &httptest.Server{Listener: ln, Config: &http.Server{Handler: nodesHandler}}
	daemonServer.Start()
	defer daemonServer.Close()

	peerServer := &httptest.Server{Listener: peerLn, Config: &http.Server{Handler: peerHandler}}
	peerServer.Start()
	defer peerServer.Close()

	// Write mesh config pointing to our fake daemon.
	meshCfgDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(meshCfgDir, 0o755)
	meshCfgContent := "name = \"self-node\"\nport = " + strings.Replace(ln.Addr().String(), "127.0.0.1:", "", 1) + "\nroles = [\"controller\", \"store\"]\nshelf_root = \"" + shelfRoot + "\"\n"
	os.WriteFile(filepath.Join(meshCfgDir, "config.toml"), []byte(meshCfgContent), 0o644)

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdResolve([]string{"bartowski/Llama-3.2-1B-Instruct-GGUF", "--quant", "Q4_K_M", "--no-download", "--json"})

	w.Close()
	os.Stdout = oldStdout
	outBytes, _ := io.ReadAll(r)
	output := string(outBytes)

	// Should exit 2 (missing locally — model exists on mesh peer).
	if code != 2 {
		t.Errorf("expected exit code 2 (missing locally), got %d", code)
	}

	var result resolver.ResolveResult
	if err := json.Unmarshal(outBytes, &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v\noutput: %s", err, output)
	}
	if result.Status != "missing_locally" {
		t.Errorf("expected status 'missing_locally', got %q", result.Status)
	}
	if result.Source != "mesh" {
		t.Errorf("expected source 'mesh', got %q", result.Source)
	}
	if len(result.MeshAvailable) == 0 {
		t.Fatalf("expected mesh_available to contain entries, got empty")
	}
	if result.MeshAvailable[0].Node != "mini1" {
		t.Errorf("expected mesh_available[0].node to be 'mini1', got %q", result.MeshAvailable[0].Node)
	}
	if result.MeshAvailable[0].Path == "" {
		t.Errorf("expected mesh_available[0].path to be non-empty")
	}
	// Path should be a relative shelf path like "gguf/bartowski/Llama-3.2-1B-Instruct-GGUF/<file>.gguf"
	if !strings.HasPrefix(result.MeshAvailable[0].Path, "gguf/bartowski/") {
		t.Errorf("expected mesh_available[0].path to start with 'gguf/bartowski/', got %q", result.MeshAvailable[0].Path)
	}
}

// TestCmdResolve_MLXPeerReturnsMissingLocally verifies that resolve for an MLX model
// available on a mesh peer returns missing_locally without attempting a blocking inline
// pull. Directory-based transfers (MLX/safetensors) are expensive; resolve should
// report the peer and suggest using `pull` instead.
func TestCmdResolve_MLXPeerReturnsMissingLocally(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	os.MkdirAll(filepath.Join(shelfRoot, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = true\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Peer inventory reports an MLX model.
	peerInventory := []daemon.InventoryEntry{
		{RepoID: "mlx-community/Qwen3-0.6B-4bit", Format: "mlx", SizeBytes: 336000000},
	}

	peerLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	peerPort := peerLn.Addr().(*net.TCPAddr).Port

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	daemonPort := ln.Addr().(*net.TCPAddr).Port

	nodesHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/nodes":
			nodes := []daemon.MeshNode{
				{Name: "self-node", Address: "127.0.0.1", Port: daemonPort, Status: daemon.StatusOnline},
				{Name: "mini1", Address: "127.0.0.1", Port: peerPort, Status: daemon.StatusOnline},
			}
			json.NewEncoder(w).Encode(nodes)
		case "/v1/inventory":
			json.NewEncoder(w).Encode(peerInventory)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	peerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/inventory":
			json.NewEncoder(w).Encode(peerInventory)
		default:
			// Download endpoint deliberately not implemented — should not be called.
			t.Errorf("unexpected request to peer: %s %s (inline pull should be skipped for MLX)", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	daemonServer := &httptest.Server{Listener: ln, Config: &http.Server{Handler: nodesHandler}}
	daemonServer.Start()
	defer daemonServer.Close()

	peerServer := &httptest.Server{Listener: peerLn, Config: &http.Server{Handler: peerHandler}}
	peerServer.Start()
	defer peerServer.Close()

	meshCfgDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(meshCfgDir, 0o755)
	meshCfgContent := "name = \"self-node\"\nport = " + strings.Replace(ln.Addr().String(), "127.0.0.1:", "", 1) + "\nroles = [\"store\"]\nshelf_root = \"" + shelfRoot + "\"\n"
	os.WriteFile(filepath.Join(meshCfgDir, "config.toml"), []byte(meshCfgContent), 0o644)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdResolve([]string{"mlx-community/Qwen3-0.6B-4bit", "--json"})

	w.Close()
	os.Stdout = oldStdout
	outBytes, _ := io.ReadAll(r)

	// Should exit 2 (missing locally) — not block or hang.
	if code != 2 {
		t.Errorf("expected exit code 2 (missing_locally), got %d", code)
	}

	var result resolver.ResolveResult
	if err := json.Unmarshal(outBytes, &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v\noutput: %s", err, outBytes)
	}
	if result.Status != "missing_locally" {
		t.Errorf("expected status 'missing_locally', got %q", result.Status)
	}
	if result.Source != "mesh" {
		t.Errorf("expected source 'mesh', got %q", result.Source)
	}
	if len(result.MeshAvailable) == 0 {
		t.Error("expected mesh_available to contain entries")
	}
}

// TestCmdResolve_SourceHFSkipsPeers verifies that --source=hf skips mesh peer
// lookup and falls through directly to HF download (or miss if downloads disabled).
func TestCmdResolve_SourceHFSkipsPeers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	os.MkdirAll(filepath.Join(shelfRoot, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Set up a fake daemon that will fail the test if its /v1/nodes is queried.
	peerCalled := false
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	daemonPort := ln.Addr().(*net.TCPAddr).Port
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			peerCalled = true
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: handler}}
	srv.Start()
	defer srv.Close()

	// Write mesh config pointing to our fake daemon.
	meshCfgDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(meshCfgDir, 0o755)
	meshCfgContent := fmt.Sprintf("name = \"self-node\"\nport = %d\nroles = [\"store\"]\nshelf_root = \"%s\"\n", daemonPort, shelfRoot)
	os.WriteFile(filepath.Join(meshCfgDir, "config.toml"), []byte(meshCfgContent), 0o644)

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdResolve([]string{"bartowski/Llama-3.2-1B-Instruct-GGUF", "--quant", "Q4_K_M", "--source", "hf", "--no-download", "--json"})

	w.Close()
	os.Stdout = oldStdout
	io.ReadAll(r)

	// With --source=hf and --no-download, should be a plain miss (exit 1), not missing_locally.
	if code != 1 {
		t.Errorf("expected exit code 1 (missing), got %d", code)
	}
	if peerCalled {
		t.Error("--source=hf should skip mesh peer query, but /v1/nodes was called")
	}
}

// TestPullFromMeshPeer_PrintsProgress verifies that pullFromMeshPeer prints
// periodic progress lines to stderr while the transfer is in-progress, and
// no timeout message when the transfer completes in time.
func TestPullFromMeshPeer_PrintsProgress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	os.MkdirAll(filepath.Join(shelfRoot, "gguf", "unsloth", "Qwen3-0.6B-GGUF"), 0o755)

	// The model file must exist for the post-transfer local re-resolve to succeed.
	modelPath := filepath.Join(shelfRoot, "gguf", "unsloth", "Qwen3-0.6B-GGUF", "Qwen3-0.6B-Q4_K_M.gguf")
	os.WriteFile(modelPath, []byte("fake gguf"), 0o644)

	const jobID = "testjob001"
	pollCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/pull":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(daemon.PullResponse{
				JobID:  jobID,
				Status: daemon.JobQueued,
				Target: "local-node",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs":
			pollCount++
			w.Header().Set("Content-Type", "application/json")
			if pollCount <= 3 {
				// First few polls: in-progress with bytes.
				json.NewEncoder(w).Encode(daemon.Job{
					ID:              jobID,
					Status:          daemon.JobDownloading,
					BytesDownloaded: 300_000_000,
					BytesTotal:      637_000_000,
				})
			} else {
				json.NewEncoder(w).Encode(daemon.Job{
					ID:     jobID,
					Status: daemon.JobCompleted,
				})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	meshCfgDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(meshCfgDir, 0o755)
	meshCfgContent := fmt.Sprintf("name = \"local-node\"\nport = %d\nroles = [\"executor\"]\nshelf_root = %q\n", port, shelfRoot)
	os.WriteFile(filepath.Join(meshCfgDir, "config.toml"), []byte(meshCfgContent), 0o644)

	// Shrink progress interval so we get output within a few polls.
	orig := peerProgressInterval
	peerProgressInterval = 1 * time.Millisecond
	t.Cleanup(func() { peerProgressInterval = orig })

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	result := pullFromMeshPeer("unsloth/Qwen3-0.6B-GGUF", "gguf", "Q4_K_M", shelfRoot)

	w.Close()
	os.Stderr = oldStderr
	errOutput, _ := io.ReadAll(r)
	stderr := string(errOutput)

	if result == nil {
		t.Fatal("expected non-nil result on completed transfer")
	}
	if !strings.Contains(stderr, "transferring") {
		t.Errorf("expected progress line in stderr, got: %s", stderr)
	}
	if strings.Contains(stderr, "timed out") {
		t.Errorf("expected no timeout message on success, got: %s", stderr)
	}
}

// TestPullFromMeshPeer_TimeoutMessage verifies that when the transfer times out,
// a message suggesting `model-shelf pull` is printed to stderr.
func TestPullFromMeshPeer_TimeoutMessage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	os.MkdirAll(filepath.Join(shelfRoot, "gguf"), 0o755)

	const jobID = "slowjob"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/pull":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(daemon.PullResponse{
				JobID:  jobID,
				Status: daemon.JobQueued,
				Target: "local-node",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs":
			// Never completes — always downloading.
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(daemon.Job{
				ID:              jobID,
				Status:          daemon.JobDownloading,
				BytesDownloaded: 100_000_000,
				BytesTotal:      637_000_000,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	meshCfgDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(meshCfgDir, 0o755)
	meshCfgContent := fmt.Sprintf("name = \"local-node\"\nport = %d\nroles = [\"executor\"]\nshelf_root = %q\n", port, shelfRoot)
	os.WriteFile(filepath.Join(meshCfgDir, "config.toml"), []byte(meshCfgContent), 0o644)

	// Short timeout so the test finishes quickly.
	origTimeout := peerTransferTimeout
	origInterval := peerProgressInterval
	peerTransferTimeout = 200 * time.Millisecond
	peerProgressInterval = 50 * time.Millisecond
	t.Cleanup(func() {
		peerTransferTimeout = origTimeout
		peerProgressInterval = origInterval
	})

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	result := pullFromMeshPeer("unsloth/Qwen3-0.6B-GGUF", "gguf", "Q4_K_M", shelfRoot)

	w.Close()
	os.Stderr = oldStderr
	errOutput, _ := io.ReadAll(r)
	stderr := string(errOutput)

	if result != nil {
		t.Errorf("expected nil result on timeout, got %+v", result)
	}
	if !strings.Contains(stderr, "timed out") {
		t.Errorf("expected timeout message in stderr, got: %s", stderr)
	}
	if !strings.Contains(stderr, "model-shelf pull") {
		t.Errorf("expected 'model-shelf pull' suggestion in stderr, got: %s", stderr)
	}
}

// TestCmdResolve_InvalidSource verifies that an invalid --source value produces an error.
func TestCmdResolve_InvalidSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	os.MkdirAll(filepath.Join(shelfRoot, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Capture stderr.
	oldErr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := cmdResolve([]string{"unsloth/Qwen3-0.6B-GGUF", "--quant", "Q4_K_M", "--source", "invalid"})

	w.Close()
	os.Stderr = oldErr
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(string(out), "--source must be one of") {
		t.Errorf("expected validation error about --source, got: %q", string(out))
	}
}
