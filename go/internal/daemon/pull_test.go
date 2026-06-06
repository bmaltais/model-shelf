package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestHandlePull_Success(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	// Create shelf format dirs.
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

	reqBody := PullRequest{
		RepoID: "not-a-valid-repo",
		Format: "mlx",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/pull", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	d.handlePull(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp PullResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.JobID == "" {
		t.Fatal("expected non-empty job_id")
	}
	if resp.Status != JobQueued {
		t.Fatalf("expected status %q, got %q", JobQueued, resp.Status)
	}
	if resp.Target != "test-node" {
		t.Fatalf("expected target %q, got %q", "test-node", resp.Target)
	}

	// Wait briefly for the background goroutine to finish (it will fail since
	// there's no real HF server, but we just need it to complete).
	time.Sleep(100 * time.Millisecond)
}

func TestHandlePull_MissingRepoID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	reqBody := PullRequest{Format: "mlx"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/pull", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	d.handlePull(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandlePull_GGUFRequiresQuant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	reqBody := PullRequest{
		RepoID: "Qwen/Qwen3-14B-GGUF",
		Format: "gguf",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/pull", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	d.handlePull(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePull_MethodNotAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/pull", nil)
	w := httptest.NewRecorder()

	d.handlePull(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleJobs_GetByID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	// Create a job directly.
	job := d.jobs.Create("test/repo", "mlx", "", "test-node")

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs?id="+job.ID, nil)
	w := httptest.NewRecorder()

	d.handleJobs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp Job
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != job.ID {
		t.Fatalf("expected job id %q, got %q", job.ID, resp.ID)
	}
}

func TestHandleJobs_NotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs?id=nonexistent", nil)
	w := httptest.NewRecorder()

	d.handleJobs(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestJobStore_Lifecycle(t *testing.T) {
	store := NewJobStore()

	job := store.Create("test/model", "gguf", "Q4_K_M", "node-1")
	if job.Status != JobQueued {
		t.Fatalf("expected queued, got %s", job.Status)
	}

	store.SetDownloading(job.ID)
	got := store.Get(job.ID)
	if got.Status != JobDownloading {
		t.Fatalf("expected running, got %s", got.Status)
	}

	store.SetCompleted(job.ID)
	got = store.Get(job.ID)
	if got.Status != JobCompleted {
		t.Fatalf("expected done, got %s", got.Status)
	}
	if got.DoneAt == nil {
		t.Fatal("expected DoneAt to be set")
	}
}

func TestJobStore_Failed(t *testing.T) {
	store := NewJobStore()

	job := store.Create("test/model", "gguf", "Q4_K_M", "node-1")
	store.SetDownloading(job.ID)
	store.SetFailed(job.ID, "disk full")

	got := store.Get(job.ID)
	if got.Status != JobFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.Error != "disk full" {
		t.Fatalf("expected error 'disk full', got %q", got.Error)
	}
}

func TestExecutePull_InvalidRepo(t *testing.T) {
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

	// Create a job for an invalid repo ID (no slash).
	job := d.jobs.Create("invalid-repo", "mlx", "", "test-node")
	d.executePull(job.ID, "invalid-repo", "mlx", "")

	// Give it a moment — executePull runs synchronously here.
	got := d.jobs.Get(job.ID)
	if got.Status != JobFailed {
		t.Fatalf("expected failed status for invalid repo, got %s", got.Status)
	}
}

// TestPullEndpointAuth verifies that pull requires mesh key when configured.
func TestPullEndpointAuth(t *testing.T) {
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
		MeshKey:   "secret-key-123",
	}
	d := New(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/pull", d.handlePull)
	handler := d.authMiddleware(mux)

	reqBody := PullRequest{RepoID: "not-valid", Format: "mlx"}
	body, _ := json.Marshal(reqBody)

	// Without auth header.
	req := httptest.NewRequest(http.MethodPost, "/v1/pull", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", w.Code)
	}

	// With correct auth header.
	body, _ = json.Marshal(reqBody)
	req = httptest.NewRequest(http.MethodPost, "/v1/pull", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-key-123")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should not be 401. It may be 202 or 400 depending on shelf setup, but not auth error.
	if w.Code == http.StatusUnauthorized {
		t.Fatal("expected auth to pass with correct key")
	}

	// Wait for background goroutine.
	time.Sleep(100 * time.Millisecond)
}

func TestJobStore_Progress(t *testing.T) {
	store := NewJobStore()

	job := store.Create("test/model", "mlx", "", "node-1")
	store.SetDownloading(job.ID)
	store.SetProgress(job.ID, 500_000_000, 2_000_000_000)

	got := store.Get(job.ID)
	if got.BytesDownloaded != 500_000_000 {
		t.Fatalf("expected 500MB downloaded, got %d", got.BytesDownloaded)
	}
	if got.BytesTotal != 2_000_000_000 {
		t.Fatalf("expected 2GB total, got %d", got.BytesTotal)
	}
}

func TestJobStore_Pruning(t *testing.T) {
	store := NewJobStore()

	// Create a completed job with DoneAt 25 hours ago.
	job := store.Create("test/model", "mlx", "", "node-1")
	store.SetCompleted(job.ID)

	// Manually backdate the DoneAt to trigger pruning.
	store.mu.Lock()
	old := time.Now().Add(-25 * time.Hour)
	store.jobs[job.ID].DoneAt = &old
	store.mu.Unlock()

	// Create a recent job that should survive pruning.
	recentJob := store.Create("test/model2", "mlx", "", "node-1")
	store.SetCompleted(recentJob.ID)

	// All() triggers pruning.
	all := store.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 job after pruning, got %d", len(all))
	}
	if all[0].ID != recentJob.ID {
		t.Fatalf("expected recent job to survive, got %s", all[0].ID)
	}
}

func TestJobStore_Merge(t *testing.T) {
	store := NewJobStore()

	// Create a local job.
	local := store.Create("local/model", "mlx", "", "node-1")
	store.SetDownloading(local.ID)

	// Merge remote jobs.
	now := time.Now()
	remoteJobs := []Job{
		{
			ID:        "remote-job-1",
			RepoID:    "remote/model",
			Format:    "gguf",
			Quant:     "Q4_K_M",
			Target:    "node-2",
			Status:    JobCompleted,
			CreatedAt: now,
			DoneAt:    &now,
		},
	}
	store.Merge(remoteJobs)

	all := store.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 jobs after merge, got %d", len(all))
	}

	// Verify remote job was added.
	remote := store.Get("remote-job-1")
	if remote == nil {
		t.Fatal("expected remote job to be in store")
	}
	if remote.Status != JobCompleted {
		t.Fatalf("expected completed, got %s", remote.Status)
	}
}

func TestJobStore_Transferring(t *testing.T) {
	store := NewJobStore()

	job := store.Create("test/model", "mlx", "", "node-1")
	store.SetTransferring(job.ID)

	got := store.Get(job.ID)
	if got.Status != JobTransferring {
		t.Fatalf("expected transferring, got %s", got.Status)
	}
}

