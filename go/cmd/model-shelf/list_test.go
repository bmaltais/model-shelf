package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCmdList_JSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a shelf with one model.
	shelfRoot := filepath.Join(home, "shelf")
	modelDir := filepath.Join(shelfRoot, "gguf", "publisher", "model-GGUF")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.gguf"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create other format dirs (empty).
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	// Write config pointing to the shelf.
	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdList([]string{"--json"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var entries []ShelfEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, out)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.RepoID != "publisher/model-GGUF" {
		t.Errorf("repo_id = %q, want %q", e.RepoID, "publisher/model-GGUF")
	}
	if e.Format != "gguf" {
		t.Errorf("format = %q, want %q", e.Format, "gguf")
	}
	if e.SizeBytes != 4 {
		t.Errorf("size_bytes = %d, want 4", e.SizeBytes)
	}
	wantPath := filepath.Join(modelDir, "model.gguf")
	if e.Path != wantPath {
		t.Errorf("path = %q, want %q", e.Path, wantPath)
	}
}

func TestCmdList_JSON_MultipleQuants(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	modelDir := filepath.Join(shelfRoot, "gguf", "Qwen", "Qwen3-0.6B-GGUF")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two different quant files in the same repo.
	os.WriteFile(filepath.Join(modelDir, "Qwen3-0.6B-Q4_K_M.gguf"), []byte("q4data"), 0o644)
	os.WriteFile(filepath.Join(modelDir, "Qwen3-0.6B-Q8_0.gguf"), []byte("q8datadata"), 0o644)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdList([]string{"--json"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var entries []ShelfEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, out)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (one per quant), got %d: %s", len(entries), out)
	}

	// Check quants are present and distinct.
	quants := map[string]bool{}
	for _, e := range entries {
		if e.RepoID != "Qwen/Qwen3-0.6B-GGUF" {
			t.Errorf("repo_id = %q, want %q", e.RepoID, "Qwen/Qwen3-0.6B-GGUF")
		}
		if e.Quant == "" {
			t.Errorf("expected quant to be set, got empty for path %s", e.Path)
		}
		quants[e.Quant] = true
	}
	if !quants["Q4_K_M"] {
		t.Errorf("missing Q4_K_M quant in entries")
	}
	if !quants["Q8_0"] {
		t.Errorf("missing Q8_0 quant in entries")
	}
}

func TestCmdList_JSON_EmptyShelf(t *testing.T) {
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

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdList([]string{"--json"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	got := string(out)
	// Must be [] not null.
	if got != "[]\n" {
		t.Errorf("expected []\\n for empty shelf, got %q", got)
	}
}

func TestCmdList_Help(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdList([]string{"--help"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if got := string(out); len(got) == 0 {
		t.Fatal("expected help output")
	}
}
