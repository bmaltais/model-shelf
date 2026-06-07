package daemon

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alexziskind1/model-shelf/internal/selfupgrade"
	"github.com/alexziskind1/model-shelf/internal/service"
)

// upgradeRequest is the body of POST /v1/upgrade.
type upgradeRequest struct {
	Version string `json:"version"`
}

// upgradeResponse is returned by POST /v1/upgrade.
type upgradeResponse struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
}

// upgradeResponseDelay is how long the daemon waits after sending 202 before
// executing the upgrade, giving the HTTP response time to flush to the caller.
const upgradeResponseDelay = 500 * time.Millisecond

// fetchChecksums is a function variable so tests can stub it without real HTTP.
var fetchChecksums = selfupgrade.FetchChecksums

// daemonUpgrade performs download → verify → replace for the daemon.
// It is a function variable so tests can stub it without real network/disk I/O.
var daemonUpgrade = func(version, expectedSHA string) error {
	binPath, err := selfupgrade.ExecutablePath()
	if err != nil {
		return err
	}
	tmpPath, err := selfupgrade.DownloadBinary(version, runtime.GOOS, runtime.GOARCH, filepath.Dir(binPath))
	if err != nil {
		return err
	}
	// Verify before touching the existing binary; leave it untouched on mismatch.
	if err := selfupgrade.VerifyChecksum(tmpPath, expectedSHA); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return selfupgrade.ReplaceBinary(tmpPath, binPath)
}

func (d *Daemon) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req upgradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid request body"}`))
		return
	}
	if req.Version == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "version is required"}`))
		return
	}

	target := strings.TrimPrefix(req.Version, "v")
	current := strings.TrimPrefix(d.cfg.Version, "v")

	if current != "" && current == target {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(upgradeResponse{Status: "already_current"})
		return
	}

	// Fetch checksums synchronously (small file) to validate the version is
	// available for this platform before accepting the request.
	sums, err := fetchChecksums(target)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	assetName := selfupgrade.BinaryName(runtime.GOOS, runtime.GOARCH)
	expectedSHA, ok := sums[assetName]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "no binary available for " + assetName + " in checksums for v" + target,
		})
		return
	}

	// Return 202 before the upgrade runs so the caller gets a response before
	// the process restarts.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(upgradeResponse{Status: "accepted", Version: target})

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		time.Sleep(upgradeResponseDelay)
		if err := daemonUpgrade(target, expectedSHA); err != nil {
			log.Printf("model-shelf daemon: upgrade to v%s failed: %v", target, err)
			return
		}
		log.Printf("model-shelf daemon: upgraded to v%s; restarting service", target)
		if err := service.Restart(); err != nil {
			log.Printf("model-shelf daemon: could not restart service after upgrade: %v", err)
		}
	}()
}
