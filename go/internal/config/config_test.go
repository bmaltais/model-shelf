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

	missingCfg := filepath.Join(t.TempDir(), "subdir", "missing.toml")
	_, err := LoadConfig(missingCfg)
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

	missingCfg := filepath.Join(t.TempDir(), "no-such-parent", "deep", "config.toml")
	_, err := LoadConfig(missingCfg)
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

func TestLoadConfig_EmptyConfigErrorsWhenNoShelf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Write an empty config (like /dev/null would produce).
	cfgPath := filepath.Join(home, "config.toml")
	os.WriteFile(cfgPath, []byte(""), 0o644)

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error when config has no shelf_root and no initialized shelf exists")
	}
	if !strings.Contains(err.Error(), "no configured shelf found") {
		t.Errorf("expected 'no configured shelf found' error, got: %s", err.Error())
	}
}

func TestLoadConfig_EmptyConfigSucceedsWithInitializedFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create the fallback shelf with format subdirectories (as if previously initialized).
	fallback := filepath.Join(home, ".cache", "model-shelf", "models")
	os.MkdirAll(filepath.Join(fallback, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(fallback, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(fallback, "safetensors"), 0o755)

	// Write an empty config.
	cfgPath := filepath.Join(home, "config.toml")
	os.WriteFile(cfgPath, []byte(""), 0o644)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ShelfRoot != fallback {
		t.Errorf("got shelf_root=%q, want %q", cfg.ShelfRoot, fallback)
	}
}
