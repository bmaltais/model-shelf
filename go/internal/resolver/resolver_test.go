package resolver

import (
	"path/filepath"
	"testing"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		repoID string
		want   string
	}{
		{"Qwen/Qwen3-14B-GGUF", "gguf"},
		{"bartowski/Qwen3-8B-GGUF", "gguf"},
		{"mlx-community/Qwen3-14B-4bit", "mlx"},
		{"Qwen/Qwen3-4B-MLX-4bit", "mlx"},
		{"lmstudio-community/X-MLX-4bit", "mlx"},
		{"Qwen/Qwen3-14B", "safetensors"},
		{"meta-llama/Llama-3.1-8B-Instruct", "safetensors"},
	}
	for _, tt := range tests {
		t.Run(tt.repoID, func(t *testing.T) {
			got := DetectFormat(tt.repoID)
			if got != tt.want {
				t.Errorf("DetectFormat(%q) = %q, want %q", tt.repoID, got, tt.want)
			}
		})
	}
}

func TestHFFilename(t *testing.T) {
	tests := []struct {
		repoID, quant, want string
	}{
		{"Qwen/Qwen3-14B-GGUF", "Q4_K_M", "Qwen3-14B-Q4_K_M.gguf"},
		{"meta-llama/Llama-3.1-8B-Instruct-GGUF", "Q5_K_M", "Llama-3.1-8B-Instruct-Q5_K_M.gguf"},
		{"bartowski/Qwen3-0.6B-Q4_K_M-GGUF", "Q4_K_M", "Qwen3-0.6B-Q4_K_M.gguf"},
	}
	for _, tt := range tests {
		t.Run(tt.repoID, func(t *testing.T) {
			got := HFFilename(tt.repoID, tt.quant)
			if got != tt.want {
				t.Errorf("HFFilename(%q, %q) = %q, want %q", tt.repoID, tt.quant, got, tt.want)
			}
		})
	}
}

func TestShelfPathGGUF(t *testing.T) {
	root := filepath.Join("test", "shelf")
	path, err := ShelfPathGGUF(root, "Qwen/Qwen3-14B-GGUF", "Q4_K_M")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "gguf", "Qwen", "Qwen3-14B-GGUF", "Qwen3-14B-Q4_K_M.gguf")
	if path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}

func TestShelfPathSnapshot(t *testing.T) {
	root := filepath.Join("test", "shelf")
	path, err := ShelfPathSnapshot(root, "mlx-community/Qwen3-14B-4bit", "mlx")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "mlx", "mlx-community", "Qwen3-14B-4bit")
	if path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}

func TestSplitRepoIDError(t *testing.T) {
	_, _, err := splitRepoID("no-slash")
	if err == nil {
		t.Error("expected error for repo_id without slash")
	}
}
