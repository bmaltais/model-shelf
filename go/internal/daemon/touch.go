package daemon

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

// TouchModel updates the last-accessed timestamp for a model in the persisted
// inventory. This is called from the CLI resolve path (local, no daemon needed).
// Uses a lock file to prevent concurrent read-modify-write races.
func TouchModel(repoID, format, quant string, sizeBytes int64) {
	release, err := acquireInventoryLock()
	if err != nil {
		log.Printf("inventory: failed to acquire lock for touch: %v", err)
		return
	}
	defer release()

	inv, err := LoadInventory()
	if err != nil {
		log.Printf("inventory: failed to load for touch: %v", err)
		return
	}
	inv.Touch(repoID, format, quant, sizeBytes)
	if err := inv.Save(); err != nil {
		log.Printf("inventory: failed to save after touch: %v", err)
	}
}

// acquireInventoryLock acquires an exclusive lock on the inventory file.
// Returns a release function. Uses a simple lock file with polling.
func acquireInventoryLock() (release func(), err error) {
	dir := meshconfig.StateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, "inventory.lock")

	const (
		pollInterval = 50 * time.Millisecond
		timeout      = 5 * time.Second
		staleAge     = 30 * time.Second
	)

	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		// Check if stale.
		if info, statErr := os.Stat(lockPath); statErr == nil {
			if time.Since(info.ModTime()) > staleAge {
				os.Remove(lockPath)
				continue
			}
		}
		if time.Now().After(deadline) {
			// Timed out — proceed without lock (best effort).
			return func() {}, nil
		}
		time.Sleep(pollInterval)
	}
}
