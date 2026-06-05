package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

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

	code := cmdInit([]string{"--role", "store", "--shelf", shelfPath, "--name", "node1"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify no mesh.key was created for non-controller role.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	if _, err := os.Stat(keyPath); err == nil {
		t.Errorf("mesh.key should not be created for store-only role")
	}
}

func TestCmdInit_ExecutorRoleDoesNotGenerateKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shelfPath := filepath.Join(t.TempDir(), "models")

	code := cmdInit([]string{"--role", "executor", "--shelf", shelfPath, "--name", "node1"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify no mesh.key was created for non-controller role.
	keyPath := filepath.Join(home, ".model-shelf", "mesh.key")
	if _, err := os.Stat(keyPath); err == nil {
		t.Errorf("mesh.key should not be created for executor-only role")
	}
}
