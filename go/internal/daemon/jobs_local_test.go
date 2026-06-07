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
