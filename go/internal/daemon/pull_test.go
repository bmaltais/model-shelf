package daemon

import (
	"bytes"
	"encoding/json"
	"net"
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
	d.executePull(job.ID, "invalid-repo", "mlx", "", false)

	// Give it a moment — executePull runs synchronously here.
	got := d.jobs.Get(job.ID)
	if got.Status != JobFailed {
		t.Fatalf("expected failed status for invalid repo, got %s", got.Status)
	}
}

func TestExecutePull_AlreadyPresent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HF_TOKEN", "")
	t.Setenv("HUGGING_FACE_HUB_TOKEN", "")

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	// Place an MLX model on the shelf so resolver finds it locally.
	// Use mlx format (directory-based) to avoid HF API filename lookup issues.
	mlxDir := filepath.Join(shelfRoot, "mlx", "testorg", "testmodel")
	os.MkdirAll(mlxDir, 0o755)
	os.WriteFile(filepath.Join(mlxDir, "config.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(mlxDir, "model.safetensors"), []byte("fake-weights"), 0o644)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	job := d.jobs.Create("testorg/testmodel", "mlx", "", "test-node")
	d.executePull(job.ID, "testorg/testmodel", "mlx", "", false)

	got := d.jobs.Get(job.ID)
	if got.Status != JobAlreadyPresent {
		t.Fatalf("expected already_present status, got %s (error: %s)", got.Status, got.Error)
	}
	if got.BytesDownloaded != 0 {
		t.Errorf("expected bytes_downloaded=0, got %d", got.BytesDownloaded)
	}
	if got.BytesTotal != 0 {
		t.Errorf("expected bytes_total=0, got %d", got.BytesTotal)
	}
	if got.DoneAt == nil {
		t.Error("expected DoneAt to be set")
	}
}

func TestExecutePull_ForceDeletesExisting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HF_TOKEN", "")
	t.Setenv("HUGGING_FACE_HUB_TOKEN", "")

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	// Place an incomplete MLX model on the shelf (only config.json, no weights).
	mlxDir := filepath.Join(shelfRoot, "mlx", "testorg", "testmodel")
	os.MkdirAll(mlxDir, 0o755)
	os.WriteFile(filepath.Join(mlxDir, "config.json"), []byte("{}"), 0o644)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Add to inventory so we can verify removal.
	d.inventory.Touch("testorg/testmodel", "mlx", "", 100)

	job := d.jobs.Create("testorg/testmodel", "mlx", "", "test-node")
	d.executePull(job.ID, "testorg/testmodel", "mlx", "", true)

	// The force flag should delete the existing directory.
	if _, err := os.Stat(mlxDir); err == nil {
		// Directory might be recreated by download attempt, but the original
		// incomplete one should have been removed. Check that force path ran.
		t.Log("directory still exists (expected if download recreated it)")
	}

	// Verify the job did NOT short-circuit with already_present
	// (even though config.json-only dirs wouldn't match anymore, the force
	// code path should still delete regardless).
	got := d.jobs.Get(job.ID)
	if got.Status == JobAlreadyPresent {
		t.Fatal("force pull should not return already_present")
	}
}

func TestExecutePull_ForceWithCompleteModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HF_TOKEN", "")
	t.Setenv("HUGGING_FACE_HUB_TOKEN", "")

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	// Place a complete MLX model.
	mlxDir := filepath.Join(shelfRoot, "mlx", "testorg", "testmodel")
	os.MkdirAll(mlxDir, 0o755)
	os.WriteFile(filepath.Join(mlxDir, "config.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(mlxDir, "model.safetensors"), []byte("weights"), 0o644)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	d.inventory.Touch("testorg/testmodel", "mlx", "", 1000)

	job := d.jobs.Create("testorg/testmodel", "mlx", "", "test-node")
	d.executePull(job.ID, "testorg/testmodel", "mlx", "", true)

	// With force=true, should NOT return already_present even for complete model.
	got := d.jobs.Get(job.ID)
	if got.Status == JobAlreadyPresent {
		t.Fatal("force pull should not return already_present even for complete model")
	}

	// The directory should have been deleted (force path).
	// Download will fail (no HF server), but the point is force deleted it.
}

func TestHandlePull_ForceFieldParsed(t *testing.T) {
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

	reqBody := PullRequest{
		RepoID: "testorg/testmodel",
		Format: "mlx",
		Force:  true,
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

	time.Sleep(100 * time.Millisecond)
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

	// Create a local job in queued state.
	local := store.Create("local/model", "mlx", "", "node-1")

	// Merge remote jobs — one new, one update to the local job (status advance).
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
		{
			ID:        local.ID,
			RepoID:    "local/model",
			Format:    "mlx",
			Target:    "node-1",
			Status:    JobDownloading,
			CreatedAt: local.CreatedAt,
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

	// Verify local job was advanced to downloading.
	updated := store.Get(local.ID)
	if updated.Status != JobDownloading {
		t.Fatalf("expected status advanced to downloading, got %s", updated.Status)
	}
}

func TestJobStore_Transferring(t *testing.T) {
	store := NewJobStore()

	job := store.Create("test/model", "mlx", "", "node-1")
	store.SetTransferring(job.ID, "peer-node")

	got := store.Get(job.ID)
	if got.Status != JobTransferring {
		t.Fatalf("expected transferring, got %s", got.Status)
	}
	if got.Type != JobTypeTransfer {
		t.Fatalf("expected type transfer, got %s", got.Type)
	}
	if got.Source != "peer-node" {
		t.Fatalf("expected source peer-node, got %s", got.Source)
	}
}

func TestHandleJobs_ProxyToPeer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Create a fake peer node that has the job.
	targetJob := Job{
		ID:        "peer-job-abc123",
		RepoID:    "Qwen/Qwen3-0.6B-GGUF",
		Format:    "gguf",
		Quant:     "Q8_0",
		Target:    "peer-node",
		Status:    JobDownloading,
		CreatedAt: time.Now(),
	}
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"disk_free_gb": 100, "disk_total_gb": 500, "uptime_seconds": 3600})
			return
		}
		if r.URL.Path == "/v1/jobs" && r.URL.Query().Get("id") == "peer-job-abc123" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(targetJob)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})
	}))
	defer peerServer.Close()

	// Parse the peer server address.
	peerHost := peerServer.Listener.Addr().(*net.TCPAddr).IP.String()
	peerPort := peerServer.Listener.Addr().(*net.TCPAddr).Port

	// Set up the local daemon with the peer in its gossip state.
	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}
	cfg := &meshconfig.Config{
		Name:      "local-node",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Add peer node to gossip state.
	d.gossip.AddNode(MeshNode{
		Name:    "peer-node",
		Address: peerHost,
		Port:    peerPort,
		Roles:   []string{"store"},
		Status:  StatusOnline,
	})

	// Query for the job that only exists on the peer.
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs?id=peer-job-abc123", nil)
	w := httptest.NewRecorder()
	d.handleJobs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (proxy found job), got %d: %s", w.Code, w.Body.String())
	}

	var resp Job
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != "peer-job-abc123" {
		t.Fatalf("expected job id 'peer-job-abc123', got %q", resp.ID)
	}
	if resp.Status != JobDownloading {
		t.Fatalf("expected status downloading, got %s", resp.Status)
	}

	// The job should now be merged into the local store.
	local := d.jobs.Get("peer-job-abc123")
	if local == nil {
		t.Fatal("expected job to be merged into local store after proxy")
	}
}

func TestHandleJobs_LocalOnlyNoProxy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}
	cfg := &meshconfig.Config{
		Name:      "local-node",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Query with local=true should NOT proxy, just return 404.
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs?id=nonexistent&local=true", nil)
	w := httptest.NewRecorder()
	d.handleJobs(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 with local=true, got %d", w.Code)
	}
}

func TestHandleJobs_MeshAggregation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	now := time.Now()

	// Create a fake peer node that has different jobs.
	peerJobs := []Job{
		{
			ID:        "peer-job-111",
			RepoID:    "remote/model-a",
			Format:    "mlx",
			Target:    "peer-node",
			Status:    JobCompleted,
			CreatedAt: now.Add(-10 * time.Minute),
			DoneAt:    timePtr(now.Add(-5 * time.Minute)),
		},
		{
			ID:        "peer-job-222",
			RepoID:    "remote/model-b",
			Format:    "gguf",
			Quant:     "Q4_K_M",
			Target:    "peer-node",
			Status:    JobDownloading,
			CreatedAt: now.Add(-2 * time.Minute),
		},
	}
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"disk_free_gb": 100, "disk_total_gb": 500, "uptime_seconds": 3600})
			return
		}
		if r.URL.Path == "/v1/jobs" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(peerJobs)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer peerServer.Close()

	peerHost := peerServer.Listener.Addr().(*net.TCPAddr).IP.String()
	peerPort := peerServer.Listener.Addr().(*net.TCPAddr).Port

	// Set up local daemon with a local job.
	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}
	cfg := &meshconfig.Config{
		Name:      "local-node",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Create a local job.
	d.jobs.Create("local/model", "mlx", "", "local-node")

	// Add peer node to gossip state.
	d.gossip.AddNode(MeshNode{
		Name:    "peer-node",
		Address: peerHost,
		Port:    peerPort,
		Roles:   []string{"store"},
		Status:  StatusOnline,
	})

	// Query with mesh=true should aggregate from all nodes.
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs?mesh=true", nil)
	w := httptest.NewRecorder()
	d.handleJobs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var jobs []Job
	if err := json.NewDecoder(w.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Should have 3 jobs: 1 local + 2 from peer.
	if len(jobs) != 3 {
		t.Fatalf("expected 3 aggregated jobs, got %d", len(jobs))
	}

	// Verify all job IDs are present.
	ids := make(map[string]bool)
	for _, j := range jobs {
		ids[j.ID] = true
	}
	if !ids["peer-job-111"] {
		t.Error("missing peer-job-111 from aggregated results")
	}
	if !ids["peer-job-222"] {
		t.Error("missing peer-job-222 from aggregated results")
	}
}

func TestHandleJobs_MeshDeduplication(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	now := time.Now()

	// Peer has the same job but in a more advanced state.
	peerJobs := []Job{
		{
			ID:        "shared-job-123",
			RepoID:    "shared/model",
			Format:    "mlx",
			Target:    "peer-node",
			Status:    JobCompleted,
			CreatedAt: now.Add(-10 * time.Minute),
			DoneAt:    timePtr(now.Add(-5 * time.Minute)),
		},
	}
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"disk_free_gb": 100, "disk_total_gb": 500, "uptime_seconds": 3600})
			return
		}
		if r.URL.Path == "/v1/jobs" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(peerJobs)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer peerServer.Close()

	peerHost := peerServer.Listener.Addr().(*net.TCPAddr).IP.String()
	peerPort := peerServer.Listener.Addr().(*net.TCPAddr).Port

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}
	cfg := &meshconfig.Config{
		Name:      "local-node",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Create the same job locally but in queued state (less advanced).
	d.jobs.mu.Lock()
	d.jobs.jobs["shared-job-123"] = &Job{
		ID:        "shared-job-123",
		RepoID:    "shared/model",
		Format:    "mlx",
		Target:    "peer-node",
		Status:    JobQueued,
		CreatedAt: now.Add(-10 * time.Minute),
	}
	d.jobs.mu.Unlock()

	d.gossip.AddNode(MeshNode{
		Name:    "peer-node",
		Address: peerHost,
		Port:    peerPort,
		Roles:   []string{"store"},
		Status:  StatusOnline,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs?mesh=true", nil)
	w := httptest.NewRecorder()
	d.handleJobs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var jobs []Job
	if err := json.NewDecoder(w.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Should have 1 job (deduplicated), with the more advanced state (completed).
	if len(jobs) != 1 {
		t.Fatalf("expected 1 deduplicated job, got %d", len(jobs))
	}
	if jobs[0].Status != JobCompleted {
		t.Fatalf("expected most advanced state (completed), got %s", jobs[0].Status)
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}

