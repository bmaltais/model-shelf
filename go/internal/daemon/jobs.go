package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// JobStatus describes the state of a job.
type JobStatus string

const (
	JobQueued         JobStatus = "queued"
	JobDownloading    JobStatus = "downloading"
	JobTransferring   JobStatus = "transferring"
	JobCompleted      JobStatus = "completed"
	JobAlreadyPresent JobStatus = "already_present"
	JobFailed         JobStatus = "failed"
)

// retentionPeriod is how long completed/failed jobs are kept before pruning.
const retentionPeriod = 24 * time.Hour

// Job tracks an async pull or transfer operation.
type Job struct {
	ID              string     `json:"job_id"`
	RepoID          string     `json:"repo_id"`
	Format          string     `json:"format"`
	Quant           string     `json:"quant,omitempty"`
	Target          string     `json:"target"`
	Status          JobStatus  `json:"status"`
	BytesDownloaded int64      `json:"bytes_downloaded"`
	BytesTotal      int64      `json:"bytes_total"`
	Error           string     `json:"error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	DoneAt          *time.Time `json:"done_at,omitempty"`
}

// JobStore manages pull/transfer jobs in memory.
type JobStore struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

// NewJobStore creates an empty job store.
func NewJobStore() *JobStore {
	return &JobStore{
		jobs: make(map[string]*Job),
	}
}

// Create adds a new job in queued status and returns it.
func (s *JobStore) Create(repoID, format, quant, target string) *Job {
	id := generateJobID()
	job := &Job{
		ID:        id,
		RepoID:    repoID,
		Format:    format,
		Quant:     quant,
		Target:    target,
		Status:    JobQueued,
		CreatedAt: time.Now(),
	}
	s.mu.Lock()
	s.jobs[id] = job
	s.mu.Unlock()
	return job
}

// Get returns a job by ID, or nil if not found.
func (s *JobStore) Get(id string) *Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j := s.jobs[id]
	if j == nil {
		return nil
	}
	// Return a copy.
	copy := *j
	return &copy
}

// SetDownloading marks a job as downloading.
func (s *JobStore) SetDownloading(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobDownloading
	}
}

// SetTransferring marks a job as transferring.
func (s *JobStore) SetTransferring(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobTransferring
	}
}

// SetProgress updates the bytes downloaded and total for a job.
func (s *JobStore) SetProgress(id string, downloaded, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.BytesDownloaded = downloaded
		j.BytesTotal = total
	}
}

// SetCompleted marks a job as completed successfully.
func (s *JobStore) SetCompleted(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobCompleted
		now := time.Now()
		j.DoneAt = &now
	}
}

// SetAlreadyPresent marks a job as skipped because the model was already on disk.
func (s *JobStore) SetAlreadyPresent(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobAlreadyPresent
		j.BytesDownloaded = 0
		j.BytesTotal = 0
		now := time.Now()
		j.DoneAt = &now
	}
}

// SetFailed marks a job as failed with an error message.
func (s *JobStore) SetFailed(id string, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobFailed
		j.Error = errMsg
		now := time.Now()
		j.DoneAt = &now
	}
}

// All returns all jobs, pruning expired ones first.
func (s *JobStore) All() []Job {
	s.mu.Lock()
	s.pruneLocked()
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	return out
}

// Merge adds remote jobs to the store (for gossip replication).
// Only adds jobs that don't already exist locally.
func (s *JobStore) Merge(jobs []Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range jobs {
		if _, exists := s.jobs[jobs[i].ID]; !exists {
			j := jobs[i]
			s.jobs[j.ID] = &j
		} else {
			// Update existing job if the remote version is more advanced.
			existing := s.jobs[jobs[i].ID]
			if shouldReplace(existing, &jobs[i]) {
				j := jobs[i]
				s.jobs[j.ID] = &j
			}
		}
	}
}

// jobStateOrder returns a numeric rank for each status to compare progression.
func jobStateOrder(s JobStatus) int {
	switch s {
	case JobQueued:
		return 0
	case JobDownloading, JobTransferring:
		return 1
	case JobCompleted, JobAlreadyPresent, JobFailed:
		return 2
	default:
		return -1
	}
}

// shouldReplace returns true if the remote job represents a more advanced state.
func shouldReplace(existing, remote *Job) bool {
	remoteOrder := jobStateOrder(remote.Status)
	existingOrder := jobStateOrder(existing.Status)
	if remoteOrder > existingOrder {
		return true
	}
	if remoteOrder == existingOrder && remote.BytesDownloaded > existing.BytesDownloaded {
		return true
	}
	return false
}

// pruneLocked removes completed/failed jobs older than retentionPeriod.
// Must be called with mu held for writing.
func (s *JobStore) pruneLocked() {
	cutoff := time.Now().Add(-retentionPeriod)
	for id, j := range s.jobs {
		if j.DoneAt != nil && j.DoneAt.Before(cutoff) {
			delete(s.jobs, id)
		}
	}
}

func generateJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fall back to timestamp-based ID if crypto/rand fails.
		return hex.EncodeToString([]byte(time.Now().String()))[:32]
	}
	return hex.EncodeToString(b)
}
