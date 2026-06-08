package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/alexziskind1/model-shelf/internal/resolver"
)

// PullRequest is the body of POST /v1/pull.
type PullRequest struct {
	RepoID       string `json:"repo_id"`
	Format       string `json:"format,omitempty"`
	Quant        string `json:"quant,omitempty"`
	Force        bool   `json:"force,omitempty"`
	PreferSource string `json:"prefer_source,omitempty"` // "auto" (default), "hf", "peer"
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

	// Dedup: if an identical active job already exists and --force is not set, return it.
	if !req.Force {
		if existing := d.jobs.FindActive(req.RepoID, format, req.Quant, d.cfg.Name); existing != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(PullResponse{
				JobID:  existing.ID,
				Status: JobAlreadyInProgress,
				Target: d.cfg.Name,
			})
			return
		}
	}

	// Create job and start background download.
	job := d.jobs.Create(req.RepoID, format, req.Quant, d.cfg.Name)
	jobID := job.ID

	preferSource := req.PreferSource
	if preferSource == "" {
		preferSource = "auto"
	}

	ctx := d.shutdownCtx
	if ctx == nil {
		ctx = context.Background()
	}
	d.transfers.Add(1)
	go func() {
		defer d.transfers.Done()
		d.executePull(ctx, jobID, req.RepoID, format, req.Quant, req.Force, preferSource)
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(PullResponse{
		JobID:  jobID,
		Status: JobQueued,
		Target: d.cfg.Name,
	})
}

// executePull runs the download in the background.
// It first checks if the model already exists locally, then checks if a peer
// has the model (preferring LAN transfer), and finally falls back to HF download.
// preferSource controls source selection: "auto" (default), "hf" (skip peers), "peer" (skip HF).
func (d *Daemon) executePull(ctx context.Context, jobID, repoID, format, quant string, force bool, preferSource string) {
	d.jobs.SetDownloading(jobID)
	log.Printf("pull: starting download of %s (format=%s, quant=%s, force=%v) job=%s", repoID, format, quant, force, jobID)

	// Step 0: If --force, delete existing model before checking presence.
	if force {
		if err := d.deleteExistingModel(repoID, format, quant); err != nil {
			log.Printf("pull: job %s — force delete failed: %v (proceeding anyway)", jobID, err)
		}
	}

	// Step 1: Check if the model is already on the local shelf.
	localCfg := &resolver.Config{
		ShelfRoot:      d.cfg.ShelfRoot,
		AllowDownloads: false, // Don't download yet — just check local.
	}
	localResult, err := resolver.ResolveModel(localCfg, repoID, format, quant)
	if err == nil && localResult.Status == "found" {
		d.jobs.SetAlreadyPresent(jobID)
		log.Printf("pull: job %s skipped — %s already present at %s", jobID, repoID, safeStr(localResult.Path))
		return
	}

	// Step 2: Check mesh inventory — prefer peer transfer (LAN) over HF (WAN).
	if preferSource != "hf" {
		if peer := d.findPeerWithModel(repoID, format, quant); peer != nil {
			log.Printf("pull: job %s — model found on peer %s, attempting transfer", jobID, peer.Name)
			if err := d.transferFromPeer(ctx, jobID, peer, repoID, format, quant); err != nil {
				log.Printf("pull: job %s — peer transfer from %s failed: %v, falling back to HF", jobID, peer.Name, err)
				// Fall through to HF download.
			} else {
				// Transfer succeeded — update inventory and complete.
				d.updateInventoryAfterTransfer(jobID, repoID, format, quant)
				d.jobs.SetCompleted(jobID)
				log.Printf("pull: job %s completed — %s transferred from peer %s", jobID, repoID, peer.Name)
				return
			}
		}
	}

	// Step 3: Check disk space and evict if needed before downloading.
	if err := d.evictIfNeeded(jobID, repoID, format, quant); err != nil {
		d.jobs.SetFailed(jobID, err.Error())
		log.Printf("pull: job %s failed: eviction error: %v", jobID, err)
		return
	}

	// Step 4: Fall back to downloading from Hugging Face.
	if preferSource == "peer" {
		d.jobs.SetFailed(jobID, "model not available on any mesh peer and --source=peer prevents HF download")
		log.Printf("pull: job %s failed: no peer has model, source=peer prevents HF fallback", jobID)
		return
	}
	d.jobs.SetDownloading(jobID)
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

	// If the model was already on disk (no download occurred), mark as such.
	if result.Status == "found" {
		d.jobs.SetAlreadyPresent(jobID)
		log.Printf("pull: job %s skipped — %s already present at %s", jobID, repoID, safeStr(result.Path))
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

// deleteExistingModel removes a model from the local shelf and inventory.
// Used by --force to ensure a fresh download.
func (d *Daemon) deleteExistingModel(repoID, format, quant string) error {
	var modelPath string
	var err error

	if format == "gguf" {
		modelPath, err = resolver.ShelfPathGGUF(d.cfg.ShelfRoot, repoID, quant)
	} else {
		modelPath, err = resolver.ShelfPathSnapshot(d.cfg.ShelfRoot, repoID, format)
	}
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(modelPath); statErr != nil {
		return nil // Nothing to delete.
	}

	log.Printf("pull: --force deleting existing model at %s", modelPath)
	if err := os.RemoveAll(modelPath); err != nil {
		return fmt.Errorf("removing %s: %w", modelPath, err)
	}

	// Remove from inventory.
	d.inventory.Remove(repoID, format, quant)
	release, lockErr := acquireInventoryLock()
	if lockErr == nil {
		if saveErr := d.inventory.Save(); saveErr != nil {
			log.Printf("pull: force — inventory save error: %v", saveErr)
		}
		release()
	}
	return nil
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

	// Return all jobs — optionally aggregated from all mesh nodes.
	if r.URL.Query().Get("mesh") == "true" && r.URL.Query().Get("local") != "true" {
		allJobs := d.aggregateMeshJobs()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(allJobs)
		return
	}

	// Local-only: return jobs created on this node (excludes gossip-replicated jobs).
	jobs := d.jobs.LocalAll()
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
		hostPort := net.JoinHostPort(node.Address, fmt.Sprintf("%d", node.Port))
		url := fmt.Sprintf("http://%s/v1/jobs?id=%s&local=true", hostPort, jobID)
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
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		var job Job
		if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()
		// Merge into local store so subsequent lookups are fast.
		d.jobs.Merge([]Job{job})
		return &job
	}
	return nil
}

// aggregateMeshJobs collects jobs from all mesh nodes (including local) and
// deduplicates by job ID, keeping the most advanced state for each job.
func (d *Daemon) aggregateMeshJobs() []Job {
	// Start with local jobs.
	localJobs := d.jobs.All()
	seen := make(map[string]*Job, len(localJobs))
	for i := range localJobs {
		seen[localJobs[i].ID] = &localJobs[i]
	}

	// Query each peer node for their local jobs.
	nodes := d.gossip.Nodes()
	meshKey := d.cfg.MeshKey

	for _, node := range nodes {
		if node.Name == d.cfg.Name || node.Status == StatusOffline {
			continue
		}
		hostPort := net.JoinHostPort(node.Address, fmt.Sprintf("%d", node.Port))
		url := fmt.Sprintf("http://%s/v1/jobs?local=true", hostPort)
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
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		var peerJobs []Job
		if err := json.NewDecoder(resp.Body).Decode(&peerJobs); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		for i := range peerJobs {
			j := &peerJobs[i]
			if existing, ok := seen[j.ID]; ok {
				if shouldReplace(existing, j) {
					seen[j.ID] = j
				}
			} else {
				seen[j.ID] = j
			}
		}
	}

	out := make([]Job, 0, len(seen))
	for _, j := range seen {
		out = append(out, *j)
	}
	return out
}

func safeStr(s *string) string {
	if s == nil {
		return "(nil)"
	}
	return filepath.Base(*s)
}
