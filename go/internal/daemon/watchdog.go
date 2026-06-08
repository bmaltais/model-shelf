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

	// mergedJobStalledTimeout is how long a gossip-replicated (non-local) job
	// can go without progress before being expired. 5 gossip cycles (75 s) is
	// long enough to survive transient polling gaps while catching jobs that
	// were abandoned when the target daemon restarted.
	mergedJobStalledTimeout = 5 * pollInterval

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
				d.reapStalledMergedJobs()
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

// reapStalledMergedJobs expires non-local (gossip-replicated) jobs that have
// had no progress for mergedJobStalledTimeout and whose target node is still
// online. A target that is online but no longer reports the job means the job
// was lost when the target daemon restarted.
func (d *Daemon) reapStalledMergedJobs() {
	stalled := d.jobs.StalledMergedJobs(mergedJobStalledTimeout)
	for _, j := range stalled {
		if d.gossip.IsNodeOnline(j.Target) {
			log.Printf("watchdog: merged job %s stalled (target %s online, no progress for %v), marking as failed", j.ID, j.Target, mergedJobStalledTimeout)
			d.jobs.SetFailed(j.ID, "transfer stalled: no progress for "+mergedJobStalledTimeout.String())
		}
	}
}

// cleanupPartialTransfer removes .partial files or staging directories left
// behind by a failed transfer.
// For GGUF, large partials (>= resumeThresholdBytes) are left on disk so that
// a subsequent pull can resume from offset (see ggufPartialOffset + transferGGUF).
// Small partials are still cleaned (not worth the resume RTT per design).
func (d *Daemon) cleanupPartialTransfer(j Job) {
	if j.Format == "gguf" {
		destPath, err := resolver.ShelfPathGGUF(d.cfg.ShelfRoot, j.RepoID, j.Quant)
		if err != nil {
			return
		}
		partial := destPath + resolver.PartialSuffix
		if info, statErr := os.Stat(partial); statErr == nil {
			if info.Size() >= resumeThresholdBytes {
				log.Printf("watchdog: leaving large GGUF partial %s (%d bytes) for potential resume", partial, info.Size())
			} else {
				if err := os.Remove(partial); err != nil {
					log.Printf("watchdog: failed to remove partial file %s: %v", partial, err)
				} else {
					log.Printf("watchdog: cleaned up partial file %s", partial)
				}
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
