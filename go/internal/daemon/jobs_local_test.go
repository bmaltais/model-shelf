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
	"github.com/alexziskind1/model-shelf/internal/resolver"
)

func TestHandleJobs_LocalOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Create a local job.
	localJob := d.jobs.Create("local/model", "gguf", "Q4_K_M", "test-node")

	// Merge a remote job (simulating gossip replication).
	remoteJob := Job{
		ID:        "remote-job-abcdef1234567890",
		Type:      JobTypeDownload,
		RepoID:    "remote/model",
		Format:    "mlx",
		Target:    "other-node",
		Status:    JobDownloading,
		CreatedAt: time.Now(),
		Local:     false,
	}
	d.jobs.Merge([]Job{remoteJob})

	// Query without ?mesh=true — should only return local jobs.
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	w := httptest.NewRecorder()
	d.handleJobs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var jobs []Job
	if err := json.Unmarshal(w.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(jobs) != 1 {
		t.Fatalf("expected 1 local job, got %d", len(jobs))
	}
	if jobs[0].ID != localJob.ID {
		t.Errorf("expected local job %s, got %s", localJob.ID, jobs[0].ID)
	}
}

func TestHandleJobs_LocalWithQueryParam(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Create a local job.
	d.jobs.Create("local/model", "gguf", "Q4_K_M", "test-node")

	// Merge a remote job.
	d.jobs.Merge([]Job{{
		ID:        "remote-job-1234567890abcdef",
		Type:      JobTypeDownload,
		RepoID:    "remote/model",
		Format:    "mlx",
		Target:    "other-node",
		Status:    JobCompleted,
		CreatedAt: time.Now(),
		Local:     false,
	}})

	// Query with ?local=true — should only return local jobs.
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs?local=true", nil)
	w := httptest.NewRecorder()
	d.handleJobs(w, req)

	var jobs []Job
	json.Unmarshal(w.Body.Bytes(), &jobs)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 local job with ?local=true, got %d", len(jobs))
	}
}

func TestWatchdog_CleansUpPartialFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Create a GGUF job that will stall.
	job := d.jobs.Create("Qwen/Qwen3-0.6B-GGUF", "gguf", "Q4_K_M", "test-node")
	d.jobs.SetTransferring(job.ID, "source-peer")

	// Create the .partial file.
	destPath, _ := resolver.ShelfPathGGUF(shelfRoot, "Qwen/Qwen3-0.6B-GGUF", "Q4_K_M")
	os.MkdirAll(filepath.Dir(destPath), 0o755)
	partialPath := destPath + resolver.PartialSuffix
	os.WriteFile(partialPath, []byte("partial data"), 0o644)

	// Backdate LastProgress to trigger watchdog.
	d.jobs.mu.Lock()
	d.jobs.jobs[job.ID].LastProgress = time.Now().Add(-10 * time.Minute)
	d.jobs.mu.Unlock()

	// Run the watchdog reap.
	d.reapStalledJobs()

	// Job should be failed.
	j := d.jobs.Get(job.ID)
	if j.Status != JobFailed {
		t.Errorf("expected job status failed, got %s", j.Status)
	}
	if j.Error == "" {
		t.Error("expected error message on failed job")
	}

	// Partial file should be cleaned up.
	if _, err := os.Stat(partialPath); err == nil {
		t.Error("partial file should have been removed")
	}
}

func TestWatchdog_CleansUpStagingDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Create an MLX job that will stall.
	job := d.jobs.Create("owner/mlx-model", "mlx", "", "test-node")
	d.jobs.SetTransferring(job.ID, "source-peer")

	// Create the staging directory.
	destDir, _ := resolver.ShelfPathSnapshot(shelfRoot, "owner/mlx-model", "mlx")
	stagingDir := filepath.Join(filepath.Dir(destDir), "."+filepath.Base(destDir)+".transferring")
	os.MkdirAll(stagingDir, 0o755)
	os.WriteFile(filepath.Join(stagingDir, "model.safetensors"), []byte("partial"), 0o644)

	// Backdate LastProgress.
	d.jobs.mu.Lock()
	d.jobs.jobs[job.ID].LastProgress = time.Now().Add(-10 * time.Minute)
	d.jobs.mu.Unlock()

	// Run the watchdog reap.
	d.reapStalledJobs()

	// Job should be failed.
	j := d.jobs.Get(job.ID)
	if j.Status != JobFailed {
		t.Errorf("expected job status failed, got %s", j.Status)
	}

	// Staging dir should be cleaned up.
	if _, err := os.Stat(stagingDir); err == nil {
		t.Error("staging directory should have been removed")
	}
}

func TestExpireStaleJobsForPeer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s := NewJobStore()

	// Non-local active job for peer-1 that will NOT be in the fresh list.
	staleJob := Job{
		ID:           "stale-job-001",
		Type:         JobTypeTransfer,
		RepoID:       "org/model",
		Format:       "gguf",
		Target:       "peer-1",
		Status:       JobTransferring,
		CreatedAt:    time.Now(),
		LastProgress: time.Now(),
		Local:        false,
	}
	s.mu.Lock()
	s.jobs[staleJob.ID] = &staleJob
	s.mu.Unlock()

	// Non-local active job for peer-1 that IS in the fresh list (must survive).
	freshJob := Job{
		ID:           "fresh-job-001",
		Type:         JobTypeTransfer,
		RepoID:       "org/model2",
		Format:       "mlx",
		Target:       "peer-1",
		Status:       JobDownloading,
		CreatedAt:    time.Now(),
		LastProgress: time.Now(),
		Local:        false,
	}
	s.mu.Lock()
	s.jobs[freshJob.ID] = &freshJob
	s.mu.Unlock()

	// Completed job for peer-1 (not active — must not be touched).
	completedJob := Job{
		ID:        "completed-job-001",
		Type:      JobTypeTransfer,
		RepoID:    "org/model3",
		Format:    "gguf",
		Target:    "peer-1",
		Status:    JobCompleted,
		CreatedAt: time.Now(),
		Local:     false,
	}
	s.mu.Lock()
	s.jobs[completedJob.ID] = &completedJob
	s.mu.Unlock()

	// Non-local active job targeting a different peer (must not be touched).
	otherPeerJob := Job{
		ID:           "other-peer-job-001",
		Type:         JobTypeTransfer,
		RepoID:       "org/model4",
		Format:       "gguf",
		Target:       "peer-2",
		Status:       JobTransferring,
		CreatedAt:    time.Now(),
		LastProgress: time.Now(),
		Local:        false,
	}
	s.mu.Lock()
	s.jobs[otherPeerJob.ID] = &otherPeerJob
	s.mu.Unlock()

	// Local active job targeting peer-1 (must not be touched).
	localJob := s.Create("org/local-model", "gguf", "Q4_K_M", "peer-1")
	s.SetTransferring(localJob.ID, "peer-1")

	// Expire with fresh list containing only freshJob.
	s.ExpireStaleJobsForPeer("peer-1", []Job{freshJob})

	// staleJob should be failed.
	j := s.Get(staleJob.ID)
	if j.Status != JobFailed {
		t.Errorf("stale merged job: expected failed, got %s", j.Status)
	}
	if j.Error == "" {
		t.Error("stale merged job: expected non-empty error message")
	}
	if j.DoneAt == nil {
		t.Error("stale merged job: expected DoneAt to be set")
	}

	// freshJob should remain active.
	j = s.Get(freshJob.ID)
	if j.Status != JobDownloading {
		t.Errorf("fresh merged job: expected downloading, got %s", j.Status)
	}

	// completedJob should be unchanged.
	j = s.Get(completedJob.ID)
	if j.Status != JobCompleted {
		t.Errorf("completed job: expected completed, got %s", j.Status)
	}

	// otherPeerJob should be unchanged.
	j = s.Get(otherPeerJob.ID)
	if j.Status != JobTransferring {
		t.Errorf("other peer job: expected transferring, got %s", j.Status)
	}

	// localJob should be unchanged.
	j = s.Get(localJob.ID)
	if j.Status != JobTransferring {
		t.Errorf("local job: expected transferring, got %s", j.Status)
	}
}

func TestStalledMergedJobs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s := NewJobStore()
	pastCutoff := time.Now().Add(-10 * time.Minute)

	// Non-local stalled job.
	stalledMerged := Job{
		ID:           "merged-stalled-001",
		Type:         JobTypeTransfer,
		RepoID:       "org/model",
		Format:       "gguf",
		Target:       "peer-1",
		Status:       JobTransferring,
		CreatedAt:    pastCutoff,
		LastProgress: pastCutoff,
		Local:        false,
	}
	s.mu.Lock()
	s.jobs[stalledMerged.ID] = &stalledMerged
	s.mu.Unlock()

	// Local stalled job — must NOT appear in StalledMergedJobs.
	localJob := s.Create("org/local", "gguf", "Q4_K_M", "self")
	s.SetTransferring(localJob.ID, "source")
	s.mu.Lock()
	s.jobs[localJob.ID].LastProgress = pastCutoff
	s.mu.Unlock()

	// Non-local completed job — must NOT appear.
	completedMerged := Job{
		ID:           "merged-completed-001",
		Type:         JobTypeTransfer,
		RepoID:       "org/model2",
		Format:       "gguf",
		Target:       "peer-1",
		Status:       JobCompleted,
		CreatedAt:    pastCutoff,
		LastProgress: pastCutoff,
		Local:        false,
	}
	s.mu.Lock()
	s.jobs[completedMerged.ID] = &completedMerged
	s.mu.Unlock()

	stalled := s.StalledMergedJobs(5 * time.Minute)
	if len(stalled) != 1 {
		t.Fatalf("expected 1 stalled merged job, got %d", len(stalled))
	}
	if stalled[0].ID != stalledMerged.ID {
		t.Errorf("expected job %s, got %s", stalledMerged.ID, stalled[0].ID)
	}
}
