package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	code := cmdResolve([]string{"unsloth/Qwen3-0.6B-GGUF", "--quant", "Q4_K_M", "--no-download", "--config", "/tmp/nonexistent-" + t.Name() + ".toml"})

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
