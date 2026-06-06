package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	// Should still exit 1 (missing locally) but with mesh info.
	if code != 1 {
		t.Errorf("expected exit code 1 (missing locally), got %d", code)
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
}
