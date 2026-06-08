package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestJobStore_LocalAll_ExcludesReplicated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s := NewJobStore()

	// Create a local job.
	localJob := s.Create("owner/model", "gguf", "Q4_K_M", "node1")

	// Merge a remote job (simulating gossip replication).
	remoteJob := Job{
		ID:        "remote-job-id-1234567890123456",
		Type:      JobTypeDownload,
		RepoID:    "other/model",
		Format:    "mlx",
		Target:    "node2",
		Status:    JobDownloading,
		CreatedAt: time.Now(),
		Local:     false, // Replicated from peer.
	}
	s.Merge([]Job{remoteJob})

	// All() should return both jobs.
	all := s.All()
	if len(all) != 2 {
		t.Fatalf("All() expected 2 jobs, got %d", len(all))
	}

	// LocalAll() should return only the local job.
	local := s.LocalAll()
	if len(local) != 1 {
		t.Fatalf("LocalAll() expected 1 job, got %d", len(local))
	}
	if local[0].ID != localJob.ID {
		t.Errorf("LocalAll() returned wrong job: got %s, want %s", local[0].ID, localJob.ID)
	}
}

func TestJobStore_StalledJobs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s := NewJobStore()

	// Create a job and set it to transferring with old LastProgress.
	job := s.Create("owner/model", "gguf", "Q4_K_M", "node1")
	s.SetTransferring(job.ID, "peer1")

	// Manually backdate LastProgress to simulate a stall.
	s.mu.Lock()
	s.jobs[job.ID].LastProgress = time.Now().Add(-10 * time.Minute)
	s.mu.Unlock()

	// With 5-minute timeout, this job should be stalled.
	stalled := s.StalledJobs(5 * time.Minute)
	if len(stalled) != 1 {
		t.Fatalf("StalledJobs expected 1 stalled job, got %d", len(stalled))
	}
	if stalled[0].ID != job.ID {
		t.Errorf("StalledJobs returned wrong job: got %s, want %s", stalled[0].ID, job.ID)
	}

	// A job with recent progress should NOT be stalled.
	job2 := s.Create("owner/model2", "mlx", "", "node1")
	s.SetTransferring(job2.ID, "peer2")
	// LastProgress is just now, so it shouldn't be stalled.
	stalled = s.StalledJobs(5 * time.Minute)
	if len(stalled) != 1 {
		t.Fatalf("StalledJobs expected 1 stalled job (only the old one), got %d", len(stalled))
	}
}

func TestJobStore_StalledJobs_IgnoresReplicated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s := NewJobStore()

	// Merge a remote job that looks stalled.
	remoteJob := Job{
		ID:           "remote-stalled-1234567890123456",
		Type:         JobTypeTransfer,
		RepoID:       "other/model",
		Format:       "gguf",
		Target:       "node2",
		Status:       JobTransferring,
		CreatedAt:    time.Now().Add(-20 * time.Minute),
		LastProgress: time.Now().Add(-15 * time.Minute),
		Local:        false,
	}
	s.Merge([]Job{remoteJob})

	// StalledJobs should not return replicated jobs (they are managed by their origin node).
	stalled := s.StalledJobs(5 * time.Minute)
	if len(stalled) != 0 {
		t.Fatalf("StalledJobs should not return replicated jobs, got %d", len(stalled))
	}
}

func TestJobStore_SetProgress_UpdatesLastProgress(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s := NewJobStore()
	job := s.Create("owner/model", "gguf", "Q4_K_M", "node1")

	// Backdate LastProgress.
	s.mu.Lock()
	s.jobs[job.ID].LastProgress = time.Now().Add(-10 * time.Minute)
	s.mu.Unlock()

	// SetProgress should update LastProgress.
	s.SetProgress(job.ID, 1024*1024, 10*1024*1024)

	j := s.Get(job.ID)
	if time.Since(j.LastProgress) > time.Second {
		t.Errorf("SetProgress should update LastProgress to now, got %v ago", time.Since(j.LastProgress))
	}
}

func TestReapStalledMergedJobs_TargetOnline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Start a mock health server so the gossip peer appears online.
	mockPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"peer-1","roles":["store"]}`))
	}))
	defer mockPeer.Close()
	peerHost, peerPort := parseHostPort(t, mockPeer)

	cfg := &meshconfig.Config{
		Name:      "controller",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	// Register the mock peer as online in gossip.
	d.gossip.ApplyEvent(Event{
		Type: EventJoin,
		Node: MeshNode{Name: "peer-1", Address: peerHost, Port: peerPort, Roles: []string{"store"}, Status: StatusOnline},
	})

	// Add a non-local stalled merged job targeting the online peer.
	stalledJob := Job{
		ID:           "merged-stalled-watchdog-001",
		Type:         JobTypeTransfer,
		RepoID:       "org/model",
		Format:       "gguf",
		Target:       "peer-1",
		Status:       JobTransferring,
		CreatedAt:    time.Now().Add(-10 * time.Minute),
		LastProgress: time.Now().Add(-10 * time.Minute),
		Local:        false,
	}
	d.jobs.mu.Lock()
	d.jobs.jobs[stalledJob.ID] = &stalledJob
	d.jobs.mu.Unlock()

	// Run the merged-job watchdog with a short timeout so our backdated job triggers.
	stalled := d.jobs.StalledMergedJobs(5 * time.Minute)
	if len(stalled) != 1 {
		t.Fatalf("expected 1 stalled merged job before reap, got %d", len(stalled))
	}

	d.reapStalledMergedJobs()

	j := d.jobs.Get(stalledJob.ID)
	if j.Status != JobFailed {
		t.Errorf("expected stalled merged job to be failed when target is online, got %s", j.Status)
	}
	if j.Error == "" {
		t.Error("expected non-empty error on failed merged job")
	}
}

func TestReapStalledMergedJobs_TargetOffline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "controller",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	// Add a non-local stalled job whose target is NOT in gossip (offline / unknown).
	stalledJob := Job{
		ID:           "merged-stalled-offline-001",
		Type:         JobTypeTransfer,
		RepoID:       "org/model",
		Format:       "gguf",
		Target:       "offline-peer",
		Status:       JobTransferring,
		CreatedAt:    time.Now().Add(-10 * time.Minute),
		LastProgress: time.Now().Add(-10 * time.Minute),
		Local:        false,
	}
	d.jobs.mu.Lock()
	d.jobs.jobs[stalledJob.ID] = &stalledJob
	d.jobs.mu.Unlock()

	d.reapStalledMergedJobs()

	// Target is offline/unknown — job must NOT be expired by the watchdog.
	j := d.jobs.Get(stalledJob.ID)
	if j.Status != JobTransferring {
		t.Errorf("expected stalled merged job to remain transferring when target is offline, got %s", j.Status)
	}
}

func TestJobStore_BytesTotal_ConsistentThroughLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s := NewJobStore()
	job := s.Create("owner/model", "mlx", "", "node1")

	// Simulate transfer start: set wire-size bytes_total from Content-Length.
	wireSize := int64(351_395_328) // tar archive size including headers/padding
	s.SetProgress(job.ID, 0, wireSize)

	// Simulate progress updates during transfer.
	s.SetProgress(job.ID, 100*1024*1024, wireSize)
	s.SetProgress(job.ID, 200*1024*1024, wireSize)

	// At completion, bytes_total should still equal the wire size.
	// (The fix ensures updateInventoryAfterTransfer doesn't overwrite with content size.)
	j := s.Get(job.ID)
	if j.BytesTotal != wireSize {
		t.Errorf("BytesTotal changed: got %d, want %d", j.BytesTotal, wireSize)
	}
}
