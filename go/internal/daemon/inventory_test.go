package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestInventory_TouchAndEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	inv := NewInventory()
	inv.Touch("Qwen/Qwen3-14B-GGUF", "gguf", "Q4_K_M", 1024)
	inv.Touch("mlx-community/Qwen3-14B", "mlx", "", 2048)

	entries := inv.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Touch again — should update timestamp, not add duplicate.
	inv.Touch("Qwen/Qwen3-14B-GGUF", "gguf", "Q4_K_M", 1024)
	entries = inv.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after re-touch, got %d", len(entries))
	}
}

func TestInventory_Remove(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	inv := NewInventory()
	inv.Touch("Qwen/Qwen3-14B-GGUF", "gguf", "Q4_K_M", 1024)
	inv.Remove("Qwen/Qwen3-14B-GGUF", "gguf", "Q4_K_M")

	entries := inv.Entries()
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after remove, got %d", len(entries))
	}
}

func TestInventory_Key(t *testing.T) {
	e1 := &InventoryEntry{RepoID: "a/b", Format: "gguf", Quant: "Q4_K_M"}
	e2 := &InventoryEntry{RepoID: "a/b", Format: "gguf", Quant: "Q8_0"}
	e3 := &InventoryEntry{RepoID: "a/b", Format: "mlx"}

	if e1.Key() == e2.Key() {
		t.Error("different quants should have different keys")
	}
	if e1.Key() == e3.Key() {
		t.Error("different formats should have different keys")
	}
}

func TestInventory_SaveAndLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create state dir.
	stateDir := filepath.Join(home, ".model-shelf", "state")
	os.MkdirAll(stateDir, 0o755)

	inv := NewInventory()
	inv.Touch("Qwen/Qwen3-14B-GGUF", "gguf", "Q4_K_M", 5000)

	if err := inv.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := LoadInventory()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	entries := loaded.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].RepoID != "Qwen/Qwen3-14B-GGUF" {
		t.Errorf("unexpected repo_id: %s", entries[0].RepoID)
	}
	if entries[0].SizeBytes != 5000 {
		t.Errorf("expected size 5000, got %d", entries[0].SizeBytes)
	}
}

func TestInventory_ScanShelf(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelf := t.TempDir()
	// Create shelf structure:
	// gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf
	// mlx/mlx-community/Qwen3-14B/config.json
	ggufDir := filepath.Join(shelf, "gguf", "Qwen", "Qwen3-14B-GGUF")
	os.MkdirAll(ggufDir, 0o755)
	os.WriteFile(filepath.Join(ggufDir, "Qwen3-14B-Q4_K_M.gguf"), make([]byte, 100), 0o644)

	mlxDir := filepath.Join(shelf, "mlx", "mlx-community", "Qwen3-14B")
	os.MkdirAll(mlxDir, 0o755)
	os.WriteFile(filepath.Join(mlxDir, "config.json"), []byte("{}"), 0o644)

	safetensorsDir := filepath.Join(shelf, "safetensors")
	os.MkdirAll(safetensorsDir, 0o755)

	inv := NewInventory()
	if err := inv.ScanShelf(shelf); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	entries := inv.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Check entries by key.
	found := make(map[string]bool)
	for _, e := range entries {
		found[e.Key()] = true
	}
	if !found["Qwen/Qwen3-14B-GGUF|gguf|Q4_K_M"] {
		t.Error("missing GGUF entry")
	}
	if !found["mlx-community/Qwen3-14B|mlx"] {
		t.Error("missing MLX entry")
	}
}

func TestInventory_ScanRemovesMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelf := t.TempDir()
	// Create minimal shelf structure.
	os.MkdirAll(filepath.Join(shelf, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelf, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelf, "safetensors"), 0o755)

	inv := NewInventory()
	// Add a phantom entry that doesn't exist on disk.
	inv.Touch("ghost/model", "gguf", "Q4_K_M", 999)

	if err := inv.ScanShelf(shelf); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	entries := inv.Entries()
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries (phantom removed), got %d", len(entries))
	}
}

func TestInventory_ScanPreservesTimestamp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelf := t.TempDir()
	ggufDir := filepath.Join(shelf, "gguf", "Qwen", "Model-GGUF")
	os.MkdirAll(ggufDir, 0o755)
	os.WriteFile(filepath.Join(ggufDir, "Model-Q8_0.gguf"), make([]byte, 50), 0o644)
	os.MkdirAll(filepath.Join(shelf, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelf, "safetensors"), 0o755)

	inv := NewInventory()
	// Pre-seed with an older timestamp.
	past := time.Now().Add(-24 * time.Hour)
	inv.mu.Lock()
	inv.entries["Qwen/Model-GGUF|gguf|Q8_0"] = &InventoryEntry{
		RepoID:       "Qwen/Model-GGUF",
		Format:       "gguf",
		Quant:        "Q8_0",
		SizeBytes:    50,
		LastAccessed: past,
	}
	inv.mu.Unlock()

	if err := inv.ScanShelf(shelf); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	entries := inv.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	// Timestamp should be preserved (not reset).
	if entries[0].LastAccessed.After(past.Add(time.Second)) {
		t.Error("scan should preserve existing last_accessed timestamp")
	}
}

func TestHandleInventory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelf := t.TempDir()
	ggufDir := filepath.Join(shelf, "gguf", "Qwen", "Qwen3-GGUF")
	os.MkdirAll(ggufDir, 0o755)
	os.WriteFile(filepath.Join(ggufDir, "Qwen3-Q4_K_M.gguf"), make([]byte, 200), 0o644)
	os.MkdirAll(filepath.Join(shelf, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelf, "safetensors"), 0o755)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelf,
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/inventory", nil)
	w := httptest.NewRecorder()
	d.handleInventory(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var entries []InventoryEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].RepoID != "Qwen/Qwen3-GGUF" {
		t.Errorf("unexpected repo_id: %s", entries[0].RepoID)
	}
	if entries[0].Format != "gguf" {
		t.Errorf("unexpected format: %s", entries[0].Format)
	}
}

func TestHandleInventory_MethodNotAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}, ShelfRoot: t.TempDir()}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/inventory", nil)
	w := httptest.NewRecorder()
	d.handleInventory(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestExtractQuant(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"Qwen3-14B-Q4_K_M.gguf", "Q4_K_M"},
		{"model-Q8_0.gguf", "Q8_0"},
		{"some-model-IQ4_XS.gguf", "IQ4_XS"},
		{"model-F16.gguf", "F16"},
		// Dot-delimited quant.
		{"model.v1.0.Q4_K_M.gguf", "Q4_K_M"},
		// Lowercase quant (normalized to uppercase).
		{"model-q4_k_m.gguf", "Q4_K_M"},
		// Lowercase dot-delimited.
		{"model.v2.q8_0.gguf", "Q8_0"},
		// Ensure "Qwen" is not misidentified as a quant.
		{"Qwen-14B-Q4_K_M.gguf", "Q4_K_M"},
	}
	for _, tc := range tests {
		got := ExtractQuant(tc.filename)
		if got != tc.want {
			t.Errorf("ExtractQuant(%q) = %q, want %q", tc.filename, got, tc.want)
		}
	}
}

func TestLooksLikeQuant_RejectsNonQuant(t *testing.T) {
	// "Qwen" should not be mistaken for a quant label.
	if looksLikeQuant("Qwen") {
		t.Error("looksLikeQuant(\"Qwen\") should be false")
	}
	if looksLikeQuant("Qwen3") {
		t.Error("looksLikeQuant(\"Qwen3\") should be false")
	}
	// But actual quants should match.
	if !looksLikeQuant("Q4_K_M") {
		t.Error("looksLikeQuant(\"Q4_K_M\") should be true")
	}
	if !looksLikeQuant("IQ4_XS") {
		t.Error("looksLikeQuant(\"IQ4_XS\") should be true")
	}
}

func TestInventory_ScanSkipsPartialFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelf := t.TempDir()
	ggufDir := filepath.Join(shelf, "gguf", "Qwen", "Qwen3-0.6B-GGUF")
	os.MkdirAll(ggufDir, 0o755)
	// Complete file.
	os.WriteFile(filepath.Join(ggufDir, "Qwen3-0.6B-Q8_0.gguf"), make([]byte, 100), 0o644)
	// Partial file (in-progress download) — should be excluded.
	os.WriteFile(filepath.Join(ggufDir, "Qwen3-0.6B-Q4_K_M.gguf.partial"), make([]byte, 50), 0o644)

	os.MkdirAll(filepath.Join(shelf, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelf, "safetensors"), 0o755)

	inv := NewInventory()
	if err := inv.ScanShelf(shelf); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	entries := inv.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (partial excluded), got %d", len(entries))
	}
	if entries[0].Quant != "Q8_0" {
		t.Errorf("expected Q8_0 entry, got %q", entries[0].Quant)
	}
}

func TestInventory_ScanShelf_PicksUpExternallyAdded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelf := t.TempDir()
	os.MkdirAll(filepath.Join(shelf, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelf, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelf, "safetensors"), 0o755)

	inv := NewInventory()

	// Initial scan — shelf is empty.
	if err := inv.ScanShelf(shelf); err != nil {
		t.Fatalf("initial scan failed: %v", err)
	}
	if len(inv.Entries()) != 0 {
		t.Fatalf("expected 0 entries initially, got %d", len(inv.Entries()))
	}

	// Simulate a model added externally (e.g. via resolve download).
	ggufDir := filepath.Join(shelf, "gguf", "unsloth", "Qwen3-0.6B-GGUF")
	os.MkdirAll(ggufDir, 0o755)
	os.WriteFile(filepath.Join(ggufDir, "Qwen3-0.6B-Q4_K_M.gguf"), make([]byte, 200), 0o644)

	// Re-scan — should pick up the new model.
	if err := inv.ScanShelf(shelf); err != nil {
		t.Fatalf("rescan failed: %v", err)
	}
	entries := inv.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after rescan, got %d", len(entries))
	}
	if entries[0].RepoID != "unsloth/Qwen3-0.6B-GGUF" {
		t.Errorf("expected repo_id unsloth/Qwen3-0.6B-GGUF, got %q", entries[0].RepoID)
	}
	if entries[0].Quant != "Q4_K_M" {
		t.Errorf("expected quant Q4_K_M, got %q", entries[0].Quant)
	}
	if entries[0].SizeBytes != 200 {
		t.Errorf("expected size 200, got %d", entries[0].SizeBytes)
	}
}

func TestHandleInventoryRescan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelf := t.TempDir()
	os.MkdirAll(filepath.Join(shelf, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelf, "mlx"), 0o755)
	os.MkdirAll(filepath.Join(shelf, "safetensors"), 0o755)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelf,
	}
	d := New(cfg)

	// Verify initial inventory is empty.
	if len(d.inventory.Entries()) != 0 {
		t.Fatalf("expected empty inventory initially")
	}

	// Simulate a model added externally (e.g. by resolve download).
	ggufDir := filepath.Join(shelf, "gguf", "unsloth", "Qwen3-0.6B-GGUF")
	os.MkdirAll(ggufDir, 0o755)
	os.WriteFile(filepath.Join(ggufDir, "Qwen3-0.6B-Q4_K_M.gguf"), make([]byte, 500), 0o644)

	// Before rescan, inventory should still be empty (daemon hasn't noticed).
	if len(d.inventory.Entries()) != 0 {
		t.Fatalf("expected empty inventory before rescan")
	}

	// POST /v1/inventory/rescan should trigger immediate update.
	req := httptest.NewRequest(http.MethodPost, "/v1/inventory/rescan", nil)
	w := httptest.NewRecorder()
	d.handleInventoryRescan(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Inventory should now reflect the new model.
	entries := d.inventory.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after rescan, got %d", len(entries))
	}
	if entries[0].RepoID != "unsloth/Qwen3-0.6B-GGUF" {
		t.Errorf("expected repo_id unsloth/Qwen3-0.6B-GGUF, got %q", entries[0].RepoID)
	}
}

func TestHandleInventoryRescan_MethodNotAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}, ShelfRoot: t.TempDir()}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/inventory/rescan", nil)
	w := httptest.NewRecorder()
	d.handleInventoryRescan(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestInventory_ScanSkipsDotPrefixedDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelf := t.TempDir()
	os.MkdirAll(filepath.Join(shelf, "gguf"), 0o755)
	os.MkdirAll(filepath.Join(shelf, "safetensors"), 0o755)

	// Create a complete MLX model.
	mlxDir := filepath.Join(shelf, "mlx", "mlx-community", "Qwen3-0.6B-4bit")
	os.MkdirAll(mlxDir, 0o755)
	os.WriteFile(filepath.Join(mlxDir, "config.json"), []byte("{}"), 0o644)

	// Create a dot-prefixed staging directory (simulates in-progress transfer).
	stagingDir := filepath.Join(shelf, "mlx", "mlx-community", ".Qwen3-1.7B-4bit.transferring")
	os.MkdirAll(stagingDir, 0o755)
	os.WriteFile(filepath.Join(stagingDir, "config.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(stagingDir, "model.safetensors"), make([]byte, 1000), 0o644)

	inv := NewInventory()
	if err := inv.ScanShelf(shelf); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	entries := inv.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (staging excluded), got %d", len(entries))
	}
	if entries[0].RepoID != "mlx-community/Qwen3-0.6B-4bit" {
		t.Errorf("expected mlx-community/Qwen3-0.6B-4bit, got %q", entries[0].RepoID)
	}
}
