package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// lockDir returns the path to the locks directory within the shelf root.
func lockDir(shelfRoot string) string {
	return filepath.Join(shelfRoot, ".locks")
}

// lockPath returns the lock file path for a given repo and quant/format.
func lockPath(shelfRoot, repoID, qualifier string) string {
	// Sanitize repo ID for use as filename.
	safe := strings.ReplaceAll(repoID, "/", "--")
	if qualifier != "" {
		safe += "-" + qualifier
	}
	return filepath.Join(lockDir(shelfRoot), safe+".lock")
}

// acquireLock attempts to acquire an exclusive lock file. If the lock is already
// held (file exists), it waits up to timeout for the lock to be released.
// Returns a release function that must be called when done.
func acquireLock(shelfRoot, repoID, qualifier string) (release func(), err error) {
	dir := lockDir(shelfRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := lockPath(shelfRoot, repoID, qualifier)

	// Try to acquire by creating the file exclusively.
	const (
		pollInterval = 200 * time.Millisecond
		timeout      = 10 * time.Minute // generous for large downloads
		staleAge     = 30 * time.Minute // locks older than this are considered stale
	)

	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			// Successfully acquired the lock.
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		// Lock file exists — check if stale.
		if info, statErr := os.Stat(path); statErr == nil {
			if time.Since(info.ModTime()) > staleAge {
				// Stale lock — remove and retry.
				os.Remove(path)
				continue
			}
		}
		if time.Now().After(deadline) {
			// Timed out waiting for lock.
			return nil, &DownloadLockError{RepoID: repoID}
		}
		time.Sleep(pollInterval)
	}
}

// DownloadLockError indicates another process is downloading the same model.
type DownloadLockError struct {
	RepoID string
}

func (e *DownloadLockError) Error() string {
	return "timed out waiting for concurrent download of " + e.RepoID
}
