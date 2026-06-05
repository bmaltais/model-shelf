package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_ExplicitPathNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := LoadConfig("/tmp/does-not-exist-" + t.Name() + ".toml")
	if err == nil {
		t.Fatal("expected error when explicit config path does not exist")
	}
	if !strings.Contains(err.Error(), "config file not found") {
		t.Errorf("expected 'config file not found' error, got: %s", err.Error())
	}
}

func TestLoadConfig_ExplicitPathNonExistentParent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := LoadConfig("/nonexistent/parent/config.toml")
	if err == nil {
		t.Fatal("expected error when explicit config path parent does not exist")
	}
	if !strings.Contains(err.Error(), "config file not found") {
		t.Errorf("expected 'config file not found' error, got: %s", err.Error())
	}
}

func TestLoadConfig_ExplicitPathExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgPath := filepath.Join(home, "config.toml")
	shelfRoot := filepath.Join(home, "models")
	os.MkdirAll(shelfRoot, 0o755)
	os.WriteFile(cfgPath, []byte("shelf_root = \""+shelfRoot+"\"\n"), 0o644)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ShelfRoot != shelfRoot {
		t.Errorf("got shelf_root=%q, want %q", cfg.ShelfRoot, shelfRoot)
	}
}
