package resolver_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bmaltais/model-shelf/internal/resolver"
)

func TestDetectFormat(t *testing.T) {
	cases := []struct {
		repoID string
		want   string
	}{
		{"Qwen/Qwen3-14B-GGUF", "gguf"},
		{"mlx-community/Qwen3-14B-4bit", "mlx"},
		{"Qwen/Qwen3-4B-MLX-4bit", "mlx"},
		{"lmstudio-community/X-MLX-4bit", "mlx"},
		{"Qwen/Qwen3-14B", "safetensors"},
		{"mistralai/Mistral-7B-v0.1", "safetensors"},
	}
	for _, c := range cases {
		got := resolver.DetectFormat(c.repoID)
		if got != c.want {
			t.Errorf("DetectFormat(%q) = %q, want %q", c.repoID, got, c.want)
		}
	}
}

func TestHFFilename(t *testing.T) {
	cases := []struct {
		repoID string
		quant  string
		want   string
	}{
		{"Qwen/Qwen3-14B-GGUF", "Q4_K_M", "Qwen3-14B-Q4_K_M.gguf"},
		{"Qwen/Qwen3-0.6B-Q4_K_M-GGUF", "Q4_K_M", "Qwen3-0.6B-Q4_K_M.gguf"},
		{"publisher/SomeModel-GGUF", "Q8_0", "SomeModel-Q8_0.gguf"},
	}
	for _, c := range cases {
		got := resolver.HFFilename(c.repoID, c.quant)
		if got != c.want {
			t.Errorf("HFFilename(%q, %q) = %q, want %q", c.repoID, c.quant, got, c.want)
		}
	}
}

func TestShelfPathGGUF(t *testing.T) {
	root := "/shelf"
	got := resolver.ShelfPathGGUF(root, "Qwen/Qwen3-14B-GGUF", "Q4_K_M")
	want := "/shelf/gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf"
	if got != want {
		t.Errorf("ShelfPathGGUF = %q, want %q", got, want)
	}
}

func TestShelfPathSnapshot(t *testing.T) {
	root := "/shelf"
	got := resolver.ShelfPathSnapshot(root, "mlx-community/Qwen3-4bit", "mlx")
	want := "/shelf/mlx/mlx-community/Qwen3-4bit"
	if got != want {
		t.Errorf("ShelfPathSnapshot = %q, want %q", got, want)
	}
}

func TestResolveLocal_GGUFHit(t *testing.T) {
	root := t.TempDir()
	// Plant a fake .gguf file on the shelf.
	ggufPath := filepath.Join(root, "gguf", "Qwen", "Qwen3-14B-GGUF", "Qwen3-14B-Q4_K_M.gguf")
	os.MkdirAll(filepath.Dir(ggufPath), 0o755)
	os.WriteFile(ggufPath, []byte("fake"), 0o644)

	cfg := &resolver.Config{ShelfRoot: root, AllowDownloads: false}
	result, err := resolver.ResolveLocal(cfg, "Qwen/Qwen3-14B-GGUF", "gguf", "Q4_K_M")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "found" {
		t.Errorf("Status = %q, want found", result.Status)
	}
	if result.Path != ggufPath {
		t.Errorf("Path = %q, want %q", result.Path, ggufPath)
	}
}

func TestResolveLocal_Miss(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "gguf"), 0o755)

	cfg := &resolver.Config{ShelfRoot: root, AllowDownloads: false}
	result, err := resolver.ResolveLocal(cfg, "Qwen/Qwen3-14B-GGUF", "gguf", "Q4_K_M")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "missing" {
		t.Errorf("Status = %q, want missing", result.Status)
	}
}

func TestResolveLocal_SnapshotHit(t *testing.T) {
	root := t.TempDir()
	snapshotDir := filepath.Join(root, "mlx", "mlx-community", "Qwen3-4bit")
	os.MkdirAll(snapshotDir, 0o755)
	os.WriteFile(filepath.Join(snapshotDir, "config.json"), []byte("{}"), 0o644)

	cfg := &resolver.Config{ShelfRoot: root, AllowDownloads: false}
	result, err := resolver.ResolveLocal(cfg, "mlx-community/Qwen3-4bit", "mlx", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "found" {
		t.Errorf("Status = %q, want found", result.Status)
	}
	if result.Path != snapshotDir {
		t.Errorf("Path = %q, want %q", result.Path, snapshotDir)
	}
}
