package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
	"github.com/alexziskind1/model-shelf/internal/resolver"
)

// InventoryEntry represents a single model tracked in the local inventory.
type InventoryEntry struct {
	RepoID       string    `json:"repo_id"`
	Format       string    `json:"format"`
	Quant        string    `json:"quant,omitempty"`
	SizeBytes    int64     `json:"size_bytes"`
	LastAccessed time.Time `json:"last_accessed"`
}

// Key returns the unique identity for this entry: repo_id + format + quant.
// Quant is uppercased to ensure stable keys regardless of filename casing.
func (e *InventoryEntry) Key() string {
	if e.Quant != "" {
		return e.RepoID + "|" + e.Format + "|" + strings.ToUpper(e.Quant)
	}
	return e.RepoID + "|" + e.Format
}

// Inventory manages the local model inventory with persistence.
type Inventory struct {
	mu      sync.RWMutex
	entries map[string]*InventoryEntry // keyed by InventoryEntry.Key()
}

// NewInventory creates an empty inventory.
func NewInventory() *Inventory {
	return &Inventory{
		entries: make(map[string]*InventoryEntry),
	}
}

// inventoryStatePath returns ~/.model-shelf/state/inventory.json.
func inventoryStatePath() string {
	return filepath.Join(meshconfig.StateDir(), "inventory.json")
}

// LoadInventory loads persisted inventory from disk. Returns an empty inventory
// if the file does not exist.
func LoadInventory() (*Inventory, error) {
	inv := NewInventory()
	data, err := os.ReadFile(inventoryStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return inv, nil
		}
		return nil, err
	}
	var entries []InventoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parsing inventory.json: %w", err)
	}
	for i := range entries {
		inv.entries[entries[i].Key()] = &entries[i]
	}
	return inv, nil
}

// Save persists the inventory to disk atomically.
func (inv *Inventory) Save() error {
	inv.mu.RLock()
	entries := make([]InventoryEntry, 0, len(inv.entries))
	for _, e := range inv.entries {
		entries = append(entries, *e)
	}
	inv.mu.RUnlock()

	path := inventoryStatePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Entries returns a copy of all inventory entries.
func (inv *Inventory) Entries() []InventoryEntry {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	entries := make([]InventoryEntry, 0, len(inv.entries))
	for _, e := range inv.entries {
		entries = append(entries, *e)
	}
	return entries
}

// Touch updates the last-accessed timestamp for a model. If the model is not
// in the inventory, it is added with the current timestamp.
// Quant is normalized to uppercase for consistent keying.
func (inv *Inventory) Touch(repoID, format, quant string, sizeBytes int64) {
	entry := &InventoryEntry{
		RepoID:       repoID,
		Format:       format,
		Quant:        strings.ToUpper(quant),
		SizeBytes:    sizeBytes,
		LastAccessed: time.Now(),
	}
	inv.mu.Lock()
	existing, ok := inv.entries[entry.Key()]
	if ok {
		existing.LastAccessed = entry.LastAccessed
		if sizeBytes > 0 {
			existing.SizeBytes = sizeBytes
		}
	} else {
		inv.entries[entry.Key()] = entry
	}
	inv.mu.Unlock()
}

// Remove deletes an entry from the inventory.
// Quant is normalized to uppercase to match the stored key.
func (inv *Inventory) Remove(repoID, format, quant string) {
	entry := &InventoryEntry{RepoID: repoID, Format: format, Quant: strings.ToUpper(quant)}
	inv.mu.Lock()
	delete(inv.entries, entry.Key())
	inv.mu.Unlock()
}

// ScanShelf walks the shelf directory tree and reconciles the inventory:
// - Models found on disk but not in inventory are added.
// - Models in inventory but not on disk are removed.
// Existing entries retain their last-accessed timestamps.
func (inv *Inventory) ScanShelf(shelfRoot string) error {
	if shelfRoot == "" {
		return nil
	}

	// Collect what's actually on disk.
	onDisk := make(map[string]*InventoryEntry)

	for _, format := range resolver.SupportedFormats {
		formatDir := filepath.Join(shelfRoot, format)
		if _, err := os.Stat(formatDir); err != nil {
			continue
		}

		if format == "gguf" {
			if err := scanGGUF(formatDir, onDisk); err != nil {
				return err
			}
		} else {
			if err := scanSnapshot(formatDir, format, onDisk); err != nil {
				return err
			}
		}
	}

	// Reconcile: add missing, remove stale.
	inv.mu.Lock()
	defer inv.mu.Unlock()

	// Remove entries not on disk.
	for key := range inv.entries {
		if _, found := onDisk[key]; !found {
			delete(inv.entries, key)
		}
	}

	// Add entries on disk but not in inventory.
	now := time.Now()
	for key, entry := range onDisk {
		if _, found := inv.entries[key]; !found {
			entry.LastAccessed = now
			inv.entries[key] = entry
		} else {
			// Update size in case it changed.
			inv.entries[key].SizeBytes = entry.SizeBytes
		}
	}

	return nil
}

// scanGGUF walks the gguf format directory: gguf/<publisher>/<repo>/<file>.gguf
func scanGGUF(formatDir string, onDisk map[string]*InventoryEntry) error {
	publishers, err := os.ReadDir(formatDir)
	if err != nil {
		return err
	}
	for _, pub := range publishers {
		if !pub.IsDir() || strings.HasPrefix(pub.Name(), ".") {
			continue
		}
		pubPath := filepath.Join(formatDir, pub.Name())
		repos, err := os.ReadDir(pubPath)
		if err != nil {
			continue
		}
		for _, repo := range repos {
			if !repo.IsDir() || strings.HasPrefix(repo.Name(), ".") {
				continue
			}
			repoPath := filepath.Join(pubPath, repo.Name())
			repoID := pub.Name() + "/" + repo.Name()

			// Each .gguf file is a separate quant entry.
			files, err := os.ReadDir(repoPath)
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || !strings.HasSuffix(strings.ToLower(f.Name()), ".gguf") {
					continue
				}
				// Skip in-progress downloads (.partial files).
				if strings.HasSuffix(f.Name(), resolver.PartialSuffix) {
					continue
				}
				info, err := f.Info()
				if err != nil {
					continue
				}
				quant := ExtractQuant(f.Name())
				entry := &InventoryEntry{
					RepoID:    repoID,
					Format:    "gguf",
					Quant:     quant,
					SizeBytes: info.Size(),
				}
				onDisk[entry.Key()] = entry
			}
		}
	}
	return nil
}

// scanSnapshot walks mlx or safetensors directories: <format>/<publisher>/<repo>/
func scanSnapshot(formatDir, format string, onDisk map[string]*InventoryEntry) error {
	publishers, err := os.ReadDir(formatDir)
	if err != nil {
		return err
	}
	for _, pub := range publishers {
		if !pub.IsDir() || strings.HasPrefix(pub.Name(), ".") {
			continue
		}
		pubPath := filepath.Join(formatDir, pub.Name())
		repos, err := os.ReadDir(pubPath)
		if err != nil {
			continue
		}
		for _, repo := range repos {
			if !repo.IsDir() || strings.HasPrefix(repo.Name(), ".") {
				continue
			}
			repoPath := filepath.Join(pubPath, repo.Name())
			repoID := pub.Name() + "/" + repo.Name()
			size := dirSizeBytes(repoPath)
			entry := &InventoryEntry{
				RepoID:    repoID,
				Format:    format,
				SizeBytes: size,
			}
			onDisk[entry.Key()] = entry
		}
	}
	return nil
}

// dirSizeBytes computes the total size of files in a directory tree.
func dirSizeBytes(path string) int64 {
	var total int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// ExtractQuant derives the quantization string from a GGUF filename.
// Example: "Qwen3-14B-Q4_K_M.gguf" -> "Q4_K_M"
// Handles dot-delimited names (e.g. "model.v1.0.Q4_K_M.gguf") and normalizes
// the result to uppercase for stable identity keys.
func ExtractQuant(filename string) string {
	// Strip .gguf extension (case-insensitive).
	name := filename
	if strings.HasSuffix(strings.ToLower(name), ".gguf") {
		name = name[:len(name)-5]
	}

	// Split on both dashes and dots to handle varied naming conventions.
	// Replace dots with dashes, then split on dashes.
	normalized := strings.ReplaceAll(name, ".", "-")
	parts := strings.Split(normalized, "-")

	// Common quant patterns: Q4_K_M, Q5_K_S, Q8_0, IQ4_XS, etc.
	// Look for the last segment that matches a quant pattern.
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		if looksLikeQuant(p) {
			// Include any subsequent parts that are also quant-like
			// (handles multi-segment quants like "Q4_K_M").
			result := strings.Join(parts[i:], "-")
			return strings.ToUpper(result)
		}
	}

	// Fallback: use the last segment after the last dash, uppercased.
	if len(parts) > 1 {
		return strings.ToUpper(parts[len(parts)-1])
	}
	return strings.ToUpper(name)
}

// looksLikeQuant returns true if a string looks like a GGUF quantization label.
// Requires Q<digit>, IQ<digit>, or exact matches for F16/F32/BF16 to avoid
// false positives on names like "Qwen".
func looksLikeQuant(s string) bool {
	upper := strings.ToUpper(s)
	// Q followed by a digit: Q4_K_M, Q8_0, Q5_K_S, etc.
	if len(upper) >= 2 && upper[0] == 'Q' && upper[1] >= '0' && upper[1] <= '9' {
		return true
	}
	// IQ followed by a digit: IQ4_XS, IQ3_M, etc.
	if len(upper) >= 3 && strings.HasPrefix(upper, "IQ") && upper[2] >= '0' && upper[2] <= '9' {
		return true
	}
	// Exact float format labels.
	if upper == "F16" || upper == "F32" || upper == "BF16" {
		return true
	}
	return false
}
