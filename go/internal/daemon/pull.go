package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/alexziskind1/model-shelf/internal/resolver"
)

// PullRequest is the body of POST /v1/pull.
type PullRequest struct {
	RepoID string `json:"repo_id"`
	Format string `json:"format,omitempty"`
	Quant  string `json:"quant,omitempty"`
}

// PullResponse is returned by POST /v1/pull.
type PullResponse struct {
	JobID  string    `json:"job_id"`
	Status JobStatus `json:"status"`
	Target string    `json:"target"`
}

func (d *Daemon) handlePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}
	if req.RepoID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "repo_id is required"})
		return
	}

	// Detect format if not provided.
	format := req.Format
	if format == "" {
		format = resolver.DetectFormat(req.RepoID)
	}

	// Validate format.
	valid := false
	for _, f := range resolver.SupportedFormats {
		if f == format {
			valid = true
			break
		}
	}
	if !valid {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "unsupported format: " + format})
		return
	}

	// GGUF requires quant.
	if format == "gguf" && req.Quant == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "--quant is required for gguf format"})
		return
	}

	// Create job and start background download.
	job := d.jobs.Create(req.RepoID, format, req.Quant, d.cfg.Name)
	jobID := job.ID

	go d.executePull(jobID, req.RepoID, format, req.Quant)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(PullResponse{
		JobID:  jobID,
		Status: JobQueued,
		Target: d.cfg.Name,
	})
}

// executePull runs the download in the background.
func (d *Daemon) executePull(jobID, repoID, format, quant string) {
	d.jobs.SetDownloading(jobID)
	log.Printf("pull: starting download of %s (format=%s, quant=%s) job=%s", repoID, format, quant, jobID)

	cfg := &resolver.Config{
		ShelfRoot:      d.cfg.ShelfRoot,
		AllowDownloads: true,
	}

	result, err := resolver.ResolveModel(cfg, repoID, format, quant)
	if err != nil {
		d.jobs.SetFailed(jobID, err.Error())
		log.Printf("pull: job %s failed: %v", jobID, err)
		return
	}

	if result.Status == "missing" {
		d.jobs.SetFailed(jobID, "model not available and downloads failed")
		log.Printf("pull: job %s failed: model not available", jobID)
		return
	}

	// Update inventory with lock to prevent concurrent Save races.
	if result.Path != nil {
		var size int64
		if info, err := os.Stat(*result.Path); err == nil {
			if info.IsDir() {
				size = dirSizeBytes(*result.Path)
			} else {
				size = info.Size()
			}
		}
		release, lockErr := acquireInventoryLock()
		if lockErr != nil {
			log.Printf("pull: job %s failed to acquire inventory lock: %v", jobID, lockErr)
		} else {
			d.inventory.Touch(repoID, format, quant, size)
			if err := d.inventory.Save(); err != nil {
				log.Printf("pull: job %s inventory save error: %v", jobID, err)
			}
			release()
		}
	}

	// Update progress with final size.
	if result.Path != nil {
		if info, statErr := os.Stat(*result.Path); statErr == nil {
			var sz int64
			if info.IsDir() {
				sz = dirSizeBytes(*result.Path)
			} else {
				sz = info.Size()
			}
			d.jobs.SetProgress(jobID, sz, sz)
		}
	}

	d.jobs.SetCompleted(jobID)
	log.Printf("pull: job %s completed — %s at %s", jobID, repoID, safeStr(result.Path))
}

// handleJobs returns the status of all jobs (or a single job by ID).
func (d *Daemon) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check for ?id= query param.
	if id := r.URL.Query().Get("id"); id != "" {
		job := d.jobs.Get(id)
		if job != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(job)
			return
		}
		// If local=true, only check the local store (prevents recursive proxy).
		if r.URL.Query().Get("local") == "true" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})
			return
		}
		// Job not found locally — proxy to peer nodes (handles gossip propagation window).
		if peerJob := d.proxyJobLookup(id); peerJob != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(peerJob)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})
		return
	}

	// Return all jobs.
	jobs := d.jobs.All()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

// proxyJobLookup queries peer nodes for a job by ID. Returns the first match
// or nil if no peer has the job. This handles the gossip propagation window
// where a job exists on the target node but hasn't been replicated yet.
func (d *Daemon) proxyJobLookup(jobID string) *Job {
	nodes := d.gossip.Nodes()
	meshKey := d.cfg.MeshKey

	for _, node := range nodes {
		if node.Name == d.cfg.Name || node.Status == StatusOffline {
			continue
		}
		url := fmt.Sprintf("http://%s:%d/v1/jobs?id=%s&local=true", node.Address, node.Port, jobID)
		client := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		if meshKey != "" {
			req.Header.Set("Authorization", "Bearer "+meshKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			continue
		}
		var job Job
		if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
			continue
		}
		// Merge into local store so subsequent lookups are fast.
		d.jobs.Merge([]Job{job})
		return &job
	}
	return nil
}

func safeStr(s *string) string {
	if s == nil {
		return "(nil)"
	}
	return filepath.Base(*s)
}
