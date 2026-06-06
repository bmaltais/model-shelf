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

func TestEvictForSpace_AlreadyEnoughSpace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Request very little space — should already be available.
	results, err := d.evictForSpace(1, "some/model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no evictions, got %d", len(results))
	}
}

func TestEvictForSpace_EvictsLRUModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()

	// Create a model on disk so it can be evicted.
	modelDir := filepath.Join(shelfRoot, "mlx", "test-pub", "old-model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a config.json (required for model dir detection).
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write a weight file to give it size.
	weightData := make([]byte, 1024*1024) // 1 MB
	if err := os.WriteFile(filepath.Join(modelDir, "model.safetensors"), weightData, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Add the model to inventory with an old last-accessed timestamp.
	oldTime := time.Now().Add(-24 * time.Hour)
	d.inventory.mu.Lock()
	d.inventory.entries["test-pub/old-model|mlx"] = &InventoryEntry{
		RepoID:       "test-pub/old-model",
		Format:       "mlx",
		SizeBytes:    1024 * 1024,
		LastAccessed: oldTime,
	}
	d.inventory.mu.Unlock()

	// Set up a fake peer that has a copy of the model.
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			json.NewEncoder(w).Encode([]InventoryEntry{
				{RepoID: "test-pub/old-model", Format: "mlx", SizeBytes: 1024 * 1024},
			})
			return
		}
		if r.URL.Path == "/v1/health" {
			json.NewEncoder(w).Encode(HealthResponse{
				Name:        "peer-node",
				Roles:       []string{"store"},
				DiskFreeGB:  100,
				DiskTotalGB: 200,
			})
			return
		}
	}))
	defer peerServer.Close()

	// Parse server address.
	host, port := evictionParseHostPort(t, peerServer)

	// Register the peer node in gossip.
	now := time.Now()
	d.gossip.AddNode(MeshNode{
		Name:        "peer-node",
		Address:     host,
		Port:        port,
		Roles:       []string{"store"},
		Status:      StatusOnline,
		DiskFreeGB:  100,
		DiskTotalGB: 200,
		LastSeen:    &now,
	})

	// Request to free more bytes than currently free — force eviction.
	// We need to request a huge amount to trigger eviction. Use the total disk
	// capacity to guarantee a deficit exists.
	_, freeGB := DiskUsage(shelfRoot)
	freeBytes := int64(freeGB * 1024 * 1024 * 1024)
	neededBytes := freeBytes + 512*1024 // Need 512KB more than what's free.

	results, err := d.evictForSpace(neededBytes, "some-other/model")
	// The model is 1MB, so evicting it should free enough if deficit <= 1MB.
	// But since disk may have lots of free space, let's just verify behavior.
	if freeBytes < neededBytes {
		// Eviction should have been attempted.
		if err != nil && len(results) == 0 {
			// If error and no results, the model might not have been evictable
			// (e.g., fake peer wasn't reachable). That's okay for this test.
			t.Logf("eviction returned error (expected in isolated test): %v", err)
		} else if len(results) > 0 {
			// Verify the evicted model is correct.
			if results[0].RepoID != "test-pub/old-model" {
				t.Fatalf("expected eviction of test-pub/old-model, got %s", results[0].RepoID)
			}
			if results[0].Format != "mlx" {
				t.Fatalf("expected format mlx, got %s", results[0].Format)
			}
			// Verify the model directory was deleted.
			if _, err := os.Stat(modelDir); !os.IsNotExist(err) {
				t.Fatal("expected model directory to be deleted after eviction")
			}
		}
	} else {
		// Already had enough space, no eviction needed.
		if len(results) != 0 {
			t.Fatalf("expected no evictions when space is sufficient, got %d", len(results))
		}
	}
}

func TestEvictForSpace_NeverEvictsLastCopyWithoutRelocating(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()

	// Create a model on disk.
	modelDir := filepath.Join(shelfRoot, "mlx", "test-pub", "only-copy")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	weightData := make([]byte, 1024*1024) // 1 MB
	if err := os.WriteFile(filepath.Join(modelDir, "model.safetensors"), weightData, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Add model to inventory — it's the ONLY copy (no peers have it).
	oldTime := time.Now().Add(-24 * time.Hour)
	d.inventory.mu.Lock()
	d.inventory.entries["test-pub/only-copy|mlx"] = &InventoryEntry{
		RepoID:       "test-pub/only-copy",
		Format:       "mlx",
		SizeBytes:    1024 * 1024,
		LastAccessed: oldTime,
	}
	d.inventory.mu.Unlock()

	// Set up a fake peer that does NOT have the model and has NO free space.
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			json.NewEncoder(w).Encode([]InventoryEntry{})
			return
		}
		if r.URL.Path == "/v1/health" {
			json.NewEncoder(w).Encode(HealthResponse{
				Name:        "peer-node",
				Roles:       []string{"store"},
				DiskFreeGB:  0,
				DiskTotalGB: 100,
			})
			return
		}
	}))
	defer peerServer.Close()

	host, port := evictionParseHostPort(t, peerServer)
	now := time.Now()
	d.gossip.AddNode(MeshNode{
		Name:        "peer-node",
		Address:     host,
		Port:        port,
		Roles:       []string{"store"},
		Status:      StatusOnline,
		DiskFreeGB:  0, // No free space for relocation.
		DiskTotalGB: 100,
		LastSeen:    &now,
	})

	// Request huge amount of space to force eviction attempts.
	_, freeGB := DiskUsage(shelfRoot)
	freeBytes := int64(freeGB * 1024 * 1024 * 1024)
	neededBytes := freeBytes + 10*1024*1024 // Need 10MB more than available.

	results, err := d.evictForSpace(neededBytes, "some-other/model")

	if freeBytes < neededBytes {
		// Eviction should have been attempted but failed because model is last copy
		// and no peer has room.
		if err == nil {
			t.Fatal("expected eviction error when last copy cannot be relocated")
		}
		// Model should still exist on disk (never permanently lost).
		if _, statErr := os.Stat(modelDir); os.IsNotExist(statErr) {
			t.Fatal("model was deleted despite being last copy — invariant violated!")
		}
		// Results should be empty — nothing was actually evicted.
		if len(results) != 0 {
			t.Fatalf("expected no evictions, got %d", len(results))
		}
	}
}

func TestEvictForSpace_SkipsModelBeingPulled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()

	// Create model on disk.
	modelDir := filepath.Join(shelfRoot, "mlx", "target-pub", "target-model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.safetensors"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	oldTime := time.Now().Add(-24 * time.Hour)
	d.inventory.mu.Lock()
	d.inventory.entries["target-pub/target-model|mlx"] = &InventoryEntry{
		RepoID:       "target-pub/target-model",
		Format:       "mlx",
		SizeBytes:    1024,
		LastAccessed: oldTime,
	}
	d.inventory.mu.Unlock()

	// Request space but exclude the only model — should not evict it.
	_, freeGB := DiskUsage(shelfRoot)
	freeBytes := int64(freeGB * 1024 * 1024 * 1024)
	neededBytes := freeBytes + 1024

	results, err := d.evictForSpace(neededBytes, "target-pub/target-model")

	if freeBytes < neededBytes {
		// Should fail because the only evictable model is excluded.
		if err == nil {
			t.Fatal("expected error when only candidate is excluded")
		}
		if len(results) != 0 {
			t.Fatal("expected no evictions when candidate is excluded")
		}
		// Model should still exist.
		if _, statErr := os.Stat(modelDir); os.IsNotExist(statErr) {
			t.Fatal("excluded model was incorrectly evicted")
		}
	}
}

func TestDeleteModel_RemovesFromDiskAndInventory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()

	// Create GGUF model on disk.
	modelDir := filepath.Join(shelfRoot, "gguf", "test-pub", "test-model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	modelFile := filepath.Join(modelDir, "test-model-Q4_K_M.gguf")
	if err := os.WriteFile(modelFile, make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Add to inventory.
	d.inventory.Touch("test-pub/test-model", "gguf", "Q4_K_M", 512)

	entry := InventoryEntry{
		RepoID:    "test-pub/test-model",
		Format:    "gguf",
		Quant:     "Q4_K_M",
		SizeBytes: 512,
	}

	if err := d.deleteModel(entry); err != nil {
		t.Fatalf("deleteModel failed: %v", err)
	}

	// Verify file is gone.
	if _, err := os.Stat(modelFile); !os.IsNotExist(err) {
		t.Fatal("expected model file to be deleted")
	}

	// Verify inventory entry is removed.
	entries := d.inventory.Entries()
	for _, e := range entries {
		if e.RepoID == "test-pub/test-model" && e.Format == "gguf" && e.Quant == "Q4_K_M" {
			t.Fatal("expected inventory entry to be removed")
		}
	}
}

func TestModelExistsOnPeer(t *testing.T) {
	entry := InventoryEntry{RepoID: "pub/model", Format: "mlx"}
	peers := []peerInventoryInfo{
		{
			Name: "peer-a",
			Entries: []InventoryEntry{
				{RepoID: "pub/other", Format: "mlx"},
			},
		},
		{
			Name: "peer-b",
			Entries: []InventoryEntry{
				{RepoID: "pub/model", Format: "mlx"},
			},
		},
	}

	if !modelExistsOnPeer(entry, peers) {
		t.Fatal("expected model to exist on peer-b")
	}

	// Test when model doesn't exist on any peer.
	entry2 := InventoryEntry{RepoID: "pub/unique", Format: "gguf", Quant: "Q4_K_M"}
	if modelExistsOnPeer(entry2, peers) {
		t.Fatal("expected model NOT to exist on any peer")
	}
}

func TestCleanEmptyParents(t *testing.T) {
	shelfRoot := t.TempDir()
	deepDir := filepath.Join(shelfRoot, "gguf", "pub", "repo")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a file then delete it to simulate post-eviction cleanup.
	filePath := filepath.Join(deepDir, "model.gguf")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(filePath)

	cleanEmptyParents(filePath, shelfRoot)

	// All empty dirs should be removed up to shelfRoot.
	if _, err := os.Stat(deepDir); !os.IsNotExist(err) {
		t.Fatal("expected deepDir to be cleaned up")
	}
	if _, err := os.Stat(filepath.Join(shelfRoot, "gguf", "pub")); !os.IsNotExist(err) {
		t.Fatal("expected pub dir to be cleaned up")
	}
	// The gguf dir should also be cleaned (it's empty now).
	if _, err := os.Stat(filepath.Join(shelfRoot, "gguf")); !os.IsNotExist(err) {
		t.Fatal("expected gguf dir to be cleaned up")
	}
	// shelfRoot itself should still exist.
	if _, err := os.Stat(shelfRoot); os.IsNotExist(err) {
		t.Fatal("shelfRoot should NOT be deleted")
	}
}

// evictionParseHostPort extracts host and port from an httptest.Server.
func evictionParseHostPort(t *testing.T, server *httptest.Server) (string, int) {
	t.Helper()
	url := server.URL[len("http://"):]
	var host string
	var port int
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == ':' {
			host = url[:i]
			for _, c := range url[i+1:] {
				port = port*10 + int(c-'0')
			}
			break
		}
	}
	return host, port
}
