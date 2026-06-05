package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// JobStatus describes the state of a pull job.
type JobStatus string

const (
	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
)

// Job tracks an async pull operation.
type Job struct {
	ID        string    `json:"job_id"`
	RepoID    string    `json:"repo_id"`
	Format    string    `json:"format"`
	Quant     string    `json:"quant,omitempty"`
	Target    string    `json:"target"`
	Status    JobStatus `json:"status"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	DoneAt    *time.Time `json:"done_at,omitempty"`
}

// JobStore manages pull jobs in memory.
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

// SetRunning marks a job as running.
func (s *JobStore) SetRunning(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobRunning
	}
}

// SetDone marks a job as completed successfully.
func (s *JobStore) SetDone(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobDone
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

// All returns all jobs.
func (s *JobStore) All() []Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	return out
}

func generateJobID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
