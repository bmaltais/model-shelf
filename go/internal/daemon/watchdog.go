package daemon

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/alexziskind1/model-shelf/internal/resolver"
)

const (
	// stallTimeout is how long a job can go without progress before being
	// marked as failed. 5 minutes covers slow-start scenarios while catching
	// truly stalled transfers (e.g. peer disconnected).
	stallTimeout = 5 * time.Minute

	// watchdogInterval is how often the watchdog checks for stalled jobs.
	watchdogInterval = 30 * time.Second
)

// startWatchdog launches a background goroutine that detects and fails stalled
// jobs (transferring/downloading with no progress for stallTimeout).
func (d *Daemon) startWatchdog(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				d.reapStalledJobs()
			}
		}
	}()
}

// reapStalledJobs finds local in-progress jobs that have stalled and marks
// them as failed, cleaning up any partial files.
func (d *Daemon) reapStalledJobs() {
	stalled := d.jobs.StalledJobs(stallTimeout)
	for _, j := range stalled {
		log.Printf("watchdog: job %s stalled (no progress for %v), marking as failed", j.ID, stallTimeout)
		d.cleanupPartialTransfer(j)
		d.jobs.SetFailed(j.ID, "transfer stalled: no progress for "+stallTimeout.String())
	}
}

// cleanupPartialTransfer removes .partial files or staging directories left
// behind by a failed transfer.
func (d *Daemon) cleanupPartialTransfer(j Job) {
	if j.Format == "gguf" {
		destPath, err := resolver.ShelfPathGGUF(d.cfg.ShelfRoot, j.RepoID, j.Quant)
		if err != nil {
			return
		}
		partial := destPath + resolver.PartialSuffix
		if _, statErr := os.Stat(partial); statErr == nil {
			if err := os.Remove(partial); err != nil {
				log.Printf("watchdog: failed to remove partial file %s: %v", partial, err)
			} else {
				log.Printf("watchdog: cleaned up partial file %s", partial)
			}
		}
	} else {
		// Snapshot formats use a dot-prefixed staging directory.
		destDir, err := resolver.ShelfPathSnapshot(d.cfg.ShelfRoot, j.RepoID, j.Format)
		if err != nil {
			return
		}
		stagingDir := filepath.Join(filepath.Dir(destDir), "."+filepath.Base(destDir)+".transferring")
		if _, statErr := os.Stat(stagingDir); statErr == nil {
			if err := os.RemoveAll(stagingDir); err != nil {
				log.Printf("watchdog: failed to remove staging dir %s: %v", stagingDir, err)
			} else {
				log.Printf("watchdog: cleaned up staging dir %s", stagingDir)
			}
		}
	}
}
