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
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestCmdInit_ForceWarnsWhenDaemonRunning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// Bind a fake /v1/health server on 127.0.0.1:8844 (DefaultPort).
	// Skip if the port is unavailable (e.g. real daemon running).
	ln, err := net.Listen("tcp", "127.0.0.1:8844")
	if err != nil {
		t.Skipf("port 8844 unavailable, skipping: %v", err)
	}
	server := &httptest.Server{
		Listener: ln,
		Config:   &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/health" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"name":"node1"}`))
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		})},
	}
	server.Start()
	defer server.Close()

	// First init to create config.
	code := cmdInit([]string{"--role", "controller", "--shelf", shelfPath, "--name", "node1"})
	if code != 0 {
		t.Fatalf("first init failed with code %d", code)
	}

	// Run init --force — should detect daemon on port 8844 and print warning.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code = cmdInit([]string{"--role", "controller,store", "--shelf", shelfPath, "--name", "node1", "--force"})

	w.Close()
	os.Stdout = oldStdout
	outBytes, _ := io.ReadAll(r)
	output := string(outBytes)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput: %s", code, output)
	}

	if !strings.Contains(output, "daemon is running with old config") {
		t.Errorf("expected daemon warning, got:\n%s", output)
	}
}

func TestCmdInit_RequiresShelf(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdInit([]string{"--role", "controller"})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestCmdInit_RequiresRole(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdInit([]string{"--shelf", "/tmp/test-shelf"})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestCmdInit_CreatesConfigAndShelf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	code := cmdInit([]string{"--role", "controller,store", "--shelf", shelfPath, "--name", "test-node"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify config.toml was created.
	configPath := filepath.Join(home, ".model-shelf", "config.toml")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config.toml not created: %v", err)
	}

	// Load and verify config contents.
	cfg, err := meshconfig.LoadFrom(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.Name != "test-node" {
		t.Errorf("expected name 'test-node', got %q", cfg.Name)
	}
	if cfg.ShelfRoot != shelfPath {
		t.Errorf("expected shelf_root %q, got %q", shelfPath, cfg.ShelfRoot)
	}
	if len(cfg.Roles) != 2 || cfg.Roles[0] != "controller" || cfg.Roles[1] != "store" {
		t.Errorf("unexpected roles: %v", cfg.Roles)
	}
	if cfg.Port != meshconfig.DefaultPort {
		t.Errorf("expected port %d, got %d", meshconfig.DefaultPort, cfg.Port)
	}

	// Verify mesh.key was created.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("mesh.key not created: %v", err)
	}
	if len(keyData) < 64 {
		t.Errorf("mesh key too short: %d bytes", len(keyData))
	}

	// Verify shelf directories.
	for _, format := range []string{"gguf", "mlx", "safetensors"} {
		dir := filepath.Join(shelfPath, format)
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("shelf directory %s not created: %v", format, err)
		}
	}
}

func TestCmdInit_ErrorsIfAlreadyExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// First init.
	code := cmdInit([]string{"--role", "store", "--shelf", shelfPath, "--name", "node1"})
	if code != 0 {
		t.Fatalf("first init failed with code %d", code)
	}

	// Second init without --force should error.
	code = cmdInit([]string{"--role", "store", "--shelf", shelfPath, "--name", "node1"})
	if code != 1 {
		t.Fatalf("expected exit 1 on duplicate init, got %d", code)
	}
}

func TestCmdInit_ForceOverwrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// First init.
	code := cmdInit([]string{"--role", "store", "--shelf", shelfPath, "--name", "node1"})
	if code != 0 {
		t.Fatalf("first init failed with code %d", code)
	}

	// Second init with --force should succeed.
	code = cmdInit([]string{"--role", "controller", "--shelf", shelfPath, "--name", "node2", "--force"})
	if code != 0 {
		t.Fatalf("expected exit 0 with --force, got %d", code)
	}

	// Verify new values.
	configPath := filepath.Join(home, ".model-shelf", "config.toml")
	cfg, err := meshconfig.LoadFrom(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.Name != "node2" {
		t.Errorf("expected name 'node2', got %q", cfg.Name)
	}
	if len(cfg.Roles) != 1 || cfg.Roles[0] != "controller" {
		t.Errorf("unexpected roles: %v", cfg.Roles)
	}
}

func TestCmdInit_DefaultsToHostname(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	code := cmdInit([]string{"--role", "executor", "--shelf", shelfPath})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	configPath := filepath.Join(home, ".model-shelf", "config.toml")
	cfg, err := meshconfig.LoadFrom(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	hostname := meshconfig.GetHostname()
	if cfg.Name != hostname {
		t.Errorf("expected name %q (hostname), got %q", hostname, cfg.Name)
	}
}

func TestCmdInit_RejectsUnknownRole(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdInit([]string{"--role", "invalid", "--shelf", "/tmp/x"})
	if code != 1 {
		t.Fatalf("expected exit 1 for unknown role, got %d", code)
	}
}

func TestCmdInit_RejectsEmptyRoles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdInit([]string{"--role", ",,,", "--shelf", "/tmp/x"})
	if code != 1 {
		t.Fatalf("expected exit 1 for empty roles, got %d", code)
	}
}

func TestCmdInit_DeduplicatesRoles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	code := cmdInit([]string{"--role", "store,store,controller", "--shelf", shelfPath})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	configPath := filepath.Join(home, ".model-shelf", "config.toml")
	cfg, err := meshconfig.LoadFrom(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if len(cfg.Roles) != 2 {
		t.Errorf("expected 2 deduplicated roles, got %v", cfg.Roles)
	}
}

func TestCmdInit_ForcePreservesExistingKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// First init with controller generates a key.
	code := cmdInit([]string{"--role", "controller", "--shelf", shelfPath, "--name", "node1"})
	if code != 0 {
		t.Fatalf("first init failed with code %d", code)
	}

	// Read the generated key.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	origKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("failed to read mesh.key: %v", err)
	}

	// Second init with --force should preserve the key.
	code = cmdInit([]string{"--role", "controller", "--shelf", shelfPath, "--name", "node2", "--force"})
	if code != 0 {
		t.Fatalf("force init failed with code %d", code)
	}

	newKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("failed to read mesh.key after force: %v", err)
	}
	if string(newKey) != string(origKey) {
		t.Errorf("mesh key was rotated on --force; expected preservation")
	}
}

func TestCmdInit_StoreRoleDoesNotGenerateKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// Capture stdout to verify guidance message.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdInit([]string{"--role", "store", "--shelf", shelfPath, "--name", "node1"})

	w.Close()
	os.Stdout = oldStdout
	outBytes, _ := io.ReadAll(r)
	output := string(outBytes)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify no mesh.key was created for non-controller role.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("mesh.key should not exist for store-only role, got err: %v", err)
	}

	// Verify guidance message is printed.
	if !strings.Contains(output, "model-shelf join <peer>") {
		t.Errorf("expected join guidance in output, got:\n%s", output)
	}
}

func TestCmdInit_ExecutorRoleDoesNotGenerateKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// Capture stdout to verify guidance message.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdInit([]string{"--role", "executor", "--shelf", shelfPath, "--name", "node1"})

	w.Close()
	os.Stdout = oldStdout
	outBytes, _ := io.ReadAll(r)
	output := string(outBytes)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify no mesh.key was created for non-controller role.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("mesh.key should not exist for executor-only role, got err: %v", err)
	}

	// Verify guidance message is printed.
	if !strings.Contains(output, "model-shelf join <peer>") {
		t.Errorf("expected join guidance in output, got:\n%s", output)
	}
}

func TestCmdInit_SeedAndKeyFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	code := cmdInit([]string{
		"--role", "store",
		"--shelf", shelfPath,
		"--name", "store1",
		"--seed", "ocilab1:8844,mini1:8844",
		"--key", "abc123deadbeef",
	})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify seeds are in config.
	configPath := filepath.Join(home, ".model-shelf", "config.toml")
	cfg, err := meshconfig.LoadFrom(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if len(cfg.Seeds) != 2 {
		t.Fatalf("expected 2 seeds, got %v", cfg.Seeds)
	}
	if cfg.Seeds[0] != "ocilab1:8844" || cfg.Seeds[1] != "mini1:8844" {
		t.Errorf("unexpected seeds: %v", cfg.Seeds)
	}

	// Verify mesh key was written.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("mesh.key not created: %v", err)
	}
	if strings.TrimSpace(string(keyData)) != "abc123deadbeef" {
		t.Errorf("expected key 'abc123deadbeef', got %q", strings.TrimSpace(string(keyData)))
	}
}

func TestCmdInit_SeedWithoutKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	code := cmdInit([]string{
		"--role", "store",
		"--shelf", shelfPath,
		"--name", "store1",
		"--seed", "ocilab1:8844",
	})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify seed is in config.
	configPath := filepath.Join(home, ".model-shelf", "config.toml")
	cfg, err := meshconfig.LoadFrom(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if len(cfg.Seeds) != 1 || cfg.Seeds[0] != "ocilab1:8844" {
		t.Errorf("unexpected seeds: %v", cfg.Seeds)
	}

	// No key should be written (store role, no --key).
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("mesh.key should not exist without --key flag for store role")
	}
}

func TestCmdInit_ForceNameChangeRemovesOldNodeFromState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// First init with name "localtest".
	code := cmdInit([]string{"--role", "controller,store", "--shelf", shelfPath, "--name", "localtest"})
	if code != 0 {
		t.Fatalf("first init failed with code %d", code)
	}

	// Simulate mesh state with the old name and another peer.
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	nodes := []daemon.MeshNode{
		{Name: "localtest", Address: "ocilab1", Port: 8844, Status: daemon.StatusOnline},
		{Name: "mini1", Address: "mini1", Port: 8844, Status: daemon.StatusOnline},
	}
	data, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), data, 0o600)

	// Re-init with --force and a new name.
	code = cmdInit([]string{"--role", "controller,store", "--shelf", shelfPath, "--name", "ocilab1", "--force"})
	if code != 0 {
		t.Fatalf("force init with new name failed with code %d", code)
	}

	// Verify the old node name was removed from local mesh state.
	stateData, err := os.ReadFile(filepath.Join(stateDir, "mesh.json"))
	if err != nil {
		t.Fatalf("failed to read mesh.json: %v", err)
	}
	var updatedNodes []daemon.MeshNode
	if err := json.Unmarshal(stateData, &updatedNodes); err != nil {
		t.Fatalf("failed to parse mesh.json: %v", err)
	}

	for _, n := range updatedNodes {
		if n.Name == "localtest" {
			t.Errorf("old node name 'localtest' should have been removed from mesh state, but it's still present")
		}
	}
	// mini1 should still be present.
	found := false
	for _, n := range updatedNodes {
		if n.Name == "mini1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("peer 'mini1' should still be in mesh state")
	}
}

func TestCmdInit_ForceNameChangeSendsLeaveEventToPeers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// First init with name "localtest".
	code := cmdInit([]string{"--role", "controller,store", "--shelf", shelfPath, "--name", "localtest"})
	if code != 0 {
		t.Fatalf("first init failed with code %d", code)
	}

	// Start a fake peer that records whether a leave event was received.
	var receivedLeave bool
	var leaveName string
	peerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events" && r.Method == http.MethodPost {
			var ev daemon.Event
			if err := json.NewDecoder(r.Body).Decode(&ev); err == nil {
				if ev.Type == daemon.EventLeave {
					receivedLeave = true
					leaveName = ev.Node.Name
				}
			}
			w.Write([]byte(`{"ok": true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	peerLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	peerPort := peerLn.Addr().(*net.TCPAddr).Port
	peerServer := &httptest.Server{Listener: peerLn, Config: &http.Server{Handler: peerHandler}}
	peerServer.Start()
	defer peerServer.Close()

	// Simulate mesh state with peer node.
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	nodes := []daemon.MeshNode{
		{Name: "localtest", Address: "127.0.0.1", Port: 8844, Status: daemon.StatusOnline},
		{Name: "mini1", Address: "127.0.0.1", Port: peerPort, Status: daemon.StatusOnline},
	}
	data, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), data, 0o600)

	// Re-init with --force and a different name.
	code = cmdInit([]string{"--role", "controller,store", "--shelf", shelfPath, "--name", "ocilab1", "--force"})
	if code != 0 {
		t.Fatalf("force init with new name failed with code %d", code)
	}

	// Verify the leave event was POSTed to the peer.
	if !receivedLeave {
		t.Errorf("expected leave event to be POSTed to peer, but none was received")
	}
	if leaveName != "localtest" {
		t.Errorf("expected leave event for 'localtest', got %q", leaveName)
	}
}

func TestCmdInit_ForceSameNameDoesNotSendLeave(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// First init with name "ocilab1".
	code := cmdInit([]string{"--role", "controller,store", "--shelf", shelfPath, "--name", "ocilab1"})
	if code != 0 {
		t.Fatalf("first init failed with code %d", code)
	}

	// Start a fake peer that records whether a leave event was received.
	var receivedLeave bool
	peerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events" && r.Method == http.MethodPost {
			var ev daemon.Event
			if err := json.NewDecoder(r.Body).Decode(&ev); err == nil {
				if ev.Type == daemon.EventLeave {
					receivedLeave = true
				}
			}
			w.Write([]byte(`{"ok": true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	peerLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	peerPort := peerLn.Addr().(*net.TCPAddr).Port
	peerServer := &httptest.Server{Listener: peerLn, Config: &http.Server{Handler: peerHandler}}
	peerServer.Start()
	defer peerServer.Close()

	// Simulate mesh state with peer node.
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	nodes := []daemon.MeshNode{
		{Name: "ocilab1", Address: "127.0.0.1", Port: 8844, Status: daemon.StatusOnline},
		{Name: "mini1", Address: "127.0.0.1", Port: peerPort, Status: daemon.StatusOnline},
	}
	data, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), data, 0o600)

	// Re-init with --force but SAME name.
	code = cmdInit([]string{"--role", "controller,store", "--shelf", shelfPath, "--name", "ocilab1", "--force"})
	if code != 0 {
		t.Fatalf("force init with same name failed with code %d", code)
	}

	// Verify NO leave event was sent.
	if receivedLeave {
		t.Errorf("did not expect leave event when name is unchanged, but one was received")
	}
}

func TestCmdInit_ForceNewKeyWarnsPeers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// First init (controller) — creates a key file.
	code := cmdInit([]string{"--role", "controller", "--shelf", shelfPath, "--name", "ocilab1"})
	if code != 0 {
		t.Fatalf("first init failed with code %d", code)
	}

	// Delete the key file to simulate a missing key scenario.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	os.Remove(keyPath)

	// Write mesh state with a known peer.
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	nodes := []daemon.MeshNode{
		{Name: "ocilab1", Address: "127.0.0.1", Port: 8844, Status: daemon.StatusOnline},
		{Name: "mini1", Address: "mini1", Port: 8844, Status: daemon.StatusOnline},
	}
	data, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), data, 0o600)

	// Capture stderr.
	oldErr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	// Capture stdout to suppress it.
	oldOut := os.Stdout
	rOut, wOut, _ := os.Pipe()
	os.Stdout = wOut

	code = cmdInit([]string{"--role", "controller", "--shelf", shelfPath, "--name", "ocilab1", "--force"})

	wErr.Close()
	os.Stderr = oldErr
	wOut.Close()
	os.Stdout = oldOut
	io.ReadAll(rOut)

	stderrBytes, _ := io.ReadAll(rErr)
	stderrOutput := string(stderrBytes)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderrOutput)
	}

	if !strings.Contains(stderrOutput, "warning") || !strings.Contains(stderrOutput, "peer") {
		t.Errorf("expected peer-orphan warning on stderr, got: %q", stderrOutput)
	}
}

func TestInitCustomConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")
	customDir := filepath.Join(t.TempDir(), "custom-mesh")
	customConfig := filepath.Join(customDir, "config.toml")

	oldOut := os.Stdout
	oldErr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	code := cmdInit([]string{
		"--role", "controller",
		"--shelf", shelfPath,
		"--name", "testnode",
		"--config", customConfig,
	})

	wOut.Close()
	wErr.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr
	io.ReadAll(rOut)
	io.ReadAll(rErr)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Config must exist at the custom path.
	if _, err := os.Stat(customConfig); err != nil {
		t.Fatalf("config not written to custom path %s: %v", customConfig, err)
	}

	// Default path must be untouched.
	defaultConfig := filepath.Join(home, ".model-shelf", "config.toml")
	if _, err := os.Stat(defaultConfig); err == nil {
		t.Errorf("default config %s should not exist when --config is provided", defaultConfig)
	}

	// Config contents must be valid and contain expected values.
	cfg, err := meshconfig.LoadFrom(customConfig)
	if err != nil {
		t.Fatalf("failed to load config from custom path: %v", err)
	}
	if cfg.Name != "testnode" {
		t.Errorf("expected name 'testnode', got %q", cfg.Name)
	}
	if cfg.ShelfRoot != shelfPath {
		t.Errorf("expected shelf_root %q, got %q", shelfPath, cfg.ShelfRoot)
	}

	// Mesh key must be written alongside the custom config, not in default dir.
	customKeyPath := filepath.Join(customDir, "mesh.key")
	if _, err := os.Stat(customKeyPath); err != nil {
		t.Errorf("mesh.key not found alongside custom config at %s: %v", customKeyPath, err)
	}
	defaultKeyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	if _, err := os.Stat(defaultKeyPath); err == nil {
		t.Errorf("default mesh.key %s should not exist when --config is provided", defaultKeyPath)
	}
}

func TestCmdInit_ForceNewKeyNoPeersNoWarn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	// First init (controller) — creates a key file.
	code := cmdInit([]string{"--role", "controller", "--shelf", shelfPath, "--name", "ocilab1"})
	if code != 0 {
		t.Fatalf("first init failed with code %d", code)
	}

	// Delete the key file.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	os.Remove(keyPath)

	// No mesh state (no peers).
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)
	nodes := []daemon.MeshNode{
		{Name: "ocilab1", Address: "127.0.0.1", Port: 8844, Status: daemon.StatusOnline},
	}
	data, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(stateDir, "mesh.json"), data, 0o600)

	// Capture stderr.
	oldErr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	oldOut := os.Stdout
	rOut, wOut, _ := os.Pipe()
	os.Stdout = wOut

	code = cmdInit([]string{"--role", "controller", "--shelf", shelfPath, "--name", "ocilab1", "--force"})

	wErr.Close()
	os.Stderr = oldErr
	wOut.Close()
	os.Stdout = oldOut
	io.ReadAll(rOut)

	stderrBytes, _ := io.ReadAll(rErr)
	stderrOutput := string(stderrBytes)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderrOutput)
	}

	if strings.Contains(stderrOutput, "warning") && strings.Contains(stderrOutput, "peer") {
		t.Errorf("expected no peer-orphan warning when no peers exist, got: %q", stderrOutput)
	}
}
