package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bmaltais/model-shelf/internal/config"
)

func TestLoadConfig_ExplicitPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	content := `shelf_root = "/tmp/myshelf"
allow_downloads = false
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadRaw(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ShelfRoot != "/tmp/myshelf" {
		t.Errorf("ShelfRoot = %q, want /tmp/myshelf", cfg.ShelfRoot)
	}
	if cfg.AllowDownloads {
		t.Error("AllowDownloads should be false")
	}
}

func TestLoadConfig_NoShelfRoot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("allow_downloads = true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadRaw(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ShelfRoot != "" {
		t.Errorf("ShelfRoot should be empty, got %q", cfg.ShelfRoot)
	}
	if !cfg.AllowDownloads {
		t.Error("AllowDownloads should default to true")
	}
}

func TestWriteConfig_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	if err := config.WriteConfig(cfgPath, "/some/shelf", true); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadRaw(cfgPath)
	if err != nil {
		t.Fatalf("load after write: %v", err)
	}
	if cfg.ShelfRoot != "/some/shelf" {
		t.Errorf("ShelfRoot = %q, want /some/shelf", cfg.ShelfRoot)
	}
	if !cfg.AllowDownloads {
		t.Error("AllowDownloads should be true")
	}
}

func TestWriteConfig_NoShelfRoot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	if err := config.WriteConfig(cfgPath, "", true); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadRaw(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ShelfRoot != "" {
		t.Errorf("ShelfRoot should be empty after writing empty root, got %q", cfg.ShelfRoot)
	}
}

func TestBootstrapDefault_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "sub", "config.toml")

	result, err := config.BootstrapDefault(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if result != cfgPath {
		t.Errorf("BootstrapDefault returned %q, want %q", result, cfgPath)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("config file not created: %v", err)
	}

	// Calling again is a no-op.
	result2, err := config.BootstrapDefault(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if result2 != cfgPath {
		t.Errorf("second call returned %q, want %q", result2, cfgPath)
	}
}
