package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestCmdList_JSON_SkipsPartialFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a shelf with a completed model and a partial download.
	shelfRoot := filepath.Join(home, "shelf")
	modelDir := filepath.Join(shelfRoot, "gguf", "Qwen", "Qwen3-0.6B-GGUF")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Completed file — should appear in list.
	os.WriteFile(filepath.Join(modelDir, "Qwen3-0.6B-Q8_0.gguf"), []byte("complete-data"), 0o644)
	// Partial file — should NOT appear in list.
	os.WriteFile(filepath.Join(modelDir, "Qwen3-0.6B-Q4_K_M.gguf.partial"), []byte("partial"), 0o644)

	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

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
		t.Fatalf("expected 1 entry (partial should be excluded), got %d: %s", len(entries), out)
	}
	if entries[0].Quant != "Q8_0" {
		t.Errorf("expected Q8_0 entry, got %q", entries[0].Quant)
	}
}

func TestCmdList_HumanReadable_ShowsQuant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	shelfRoot := filepath.Join(home, "shelf")
	modelDir := filepath.Join(shelfRoot, "gguf", "unsloth", "Qwen3-0.6B-GGUF")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(modelDir, "Qwen3-0.6B-Q4_K_M.gguf"), []byte("q4data"), 0o644)
	os.WriteFile(filepath.Join(modelDir, "Qwen3-0.6B-Q8_0.gguf"), []byte("q8datadata"), 0o644)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Capture stdout (human-readable output, no --json).
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdList([]string{})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	output := string(out)
	if !strings.Contains(output, "unsloth/Qwen3-0.6B-GGUF:Q4_K_M") {
		t.Errorf("expected quant Q4_K_M in output, got:\n%s", output)
	}
	if !strings.Contains(output, "unsloth/Qwen3-0.6B-GGUF:Q8_0") {
		t.Errorf("expected quant Q8_0 in output, got:\n%s", output)
	}
}

func TestCmdList_JSON_IncludesFallbackShelf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Primary shelf with one model.
	shelfRoot := filepath.Join(home, "shelf")
	modelDir := filepath.Join(shelfRoot, "gguf", "publisher", "model-A-GGUF")
	os.MkdirAll(modelDir, 0o755)
	os.WriteFile(filepath.Join(modelDir, "model-A-Q4_K_M.gguf"), []byte("primary"), 0o644)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	// Fallback shelf (~/.cache/model-shelf/models/) with a different model.
	fallback := filepath.Join(home, ".cache", "model-shelf", "models")
	fallbackModel := filepath.Join(fallback, "gguf", "publisher", "model-B-GGUF")
	os.MkdirAll(fallbackModel, 0o755)
	os.WriteFile(filepath.Join(fallbackModel, "model-B-Q8_0.gguf"), []byte("fallback"), 0o644)
	os.MkdirAll(filepath.Join(fallback, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(fallback, "safetensors"), 0o755)

	// Config points to primary shelf.
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
		t.Fatalf("expected exit 0, got %d: %s", code, out)
	}

	var entries []ShelfEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, out)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (primary + fallback), got %d: %s", len(entries), out)
	}

	// Check both models are present with correct shelf_root.
	found := map[string]string{}
	for _, e := range entries {
		found[e.RepoID] = e.ShelfRoot
	}
	if found["publisher/model-A-GGUF"] != shelfRoot {
		t.Errorf("model-A should be in primary shelf %q, got %q", shelfRoot, found["publisher/model-A-GGUF"])
	}
	if found["publisher/model-B-GGUF"] != fallback {
		t.Errorf("model-B should be in fallback shelf %q, got %q", fallback, found["publisher/model-B-GGUF"])
	}
}

func TestCmdList_HumanReadable_IncludesFallbackShelf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Primary shelf with one model.
	shelfRoot := filepath.Join(home, "shelf")
	modelDir := filepath.Join(shelfRoot, "gguf", "publisher", "model-A-GGUF")
	os.MkdirAll(modelDir, 0o755)
	os.WriteFile(filepath.Join(modelDir, "model-A-Q4_K_M.gguf"), []byte("primary"), 0o644)
	os.MkdirAll(filepath.Join(shelfRoot, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	// Fallback shelf with a different model.
	fallback := filepath.Join(home, ".cache", "model-shelf", "models")
	fallbackModel := filepath.Join(fallback, "gguf", "publisher", "model-B-GGUF")
	os.MkdirAll(fallbackModel, 0o755)
	os.WriteFile(filepath.Join(fallbackModel, "model-B-Q8_0.gguf"), []byte("fallback"), 0o644)
	os.MkdirAll(filepath.Join(fallback, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(fallback, "safetensors"), 0o755)

	// Config points to primary shelf.
	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"" + shelfRoot + "\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Capture stdout (human-readable).
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdList([]string{})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	output := string(out)
	if !strings.Contains(output, "shelf: "+shelfRoot) {
		t.Errorf("expected primary shelf label in output, got:\n%s", output)
	}
	if !strings.Contains(output, "shelf: "+fallback) {
		t.Errorf("expected fallback shelf label in output, got:\n%s", output)
	}
	if !strings.Contains(output, "publisher/model-A-GGUF") {
		t.Errorf("expected model-A in output, got:\n%s", output)
	}
	if !strings.Contains(output, "publisher/model-B-GGUF") {
		t.Errorf("expected model-B in output, got:\n%s", output)
	}
}

func TestCmdList_NonExistentShelfRoot_Errors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Write config pointing to a shelf that doesn't exist.
	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"/nonexistent/path/that/does/not/exist\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Redirect stderr to capture error output.
	oldStderr := os.Stderr
	re, we, _ := os.Pipe()
	os.Stderr = we

	code := cmdList([]string{})

	we.Close()
	os.Stderr = oldStderr
	errOut, _ := io.ReadAll(re)

	if code != 1 {
		t.Fatalf("expected exit 1 when shelf_root doesn't exist, got %d", code)
	}
	if !strings.Contains(string(errOut), "doesn't exist") {
		t.Errorf("expected error about missing shelf, got stderr: %q", errOut)
	}
}

func TestCmdList_NonExistentShelfRoot_Errors_JSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".config", "model-shelf")
	os.MkdirAll(cfgDir, 0o755)
	cfgContent := "shelf_root = \"/nonexistent/path/that/does/not/exist\"\nallow_downloads = false\n"
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgContent), 0o644)

	oldStderr := os.Stderr
	re, we, _ := os.Pipe()
	os.Stderr = we

	code := cmdList([]string{"--json"})

	we.Close()
	os.Stderr = oldStderr
	errOut, _ := io.ReadAll(re)

	if code != 1 {
		t.Fatalf("expected exit 1 when shelf_root doesn't exist, got %d", code)
	}
	if !strings.Contains(string(errOut), "doesn't exist") {
		t.Errorf("expected error about missing shelf, got stderr: %q", errOut)
	}
}
