package resolver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMatchGGUFFile(t *testing.T) {
	tests := []struct {
		name     string
		siblings []hfRepoFile
		quant    string
		repoID   string
		want     string
	}{
		{
			name: "standard bartowski naming",
			siblings: []hfRepoFile{
				{Filename: "Llama-3.2-1B-Instruct-Q4_K_M.gguf"},
				{Filename: "Llama-3.2-1B-Instruct-Q5_K_M.gguf"},
				{Filename: "README.md"},
			},
			quant:  "Q4_K_M",
			repoID: "bartowski/Llama-3.2-1B-Instruct-GGUF",
			want:   "Llama-3.2-1B-Instruct-Q4_K_M.gguf",
		},
		{
			name: "TheBloke lowercase dot-separated",
			siblings: []hfRepoFile{
				{Filename: "tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf"},
				{Filename: "tinyllama-1.1b-chat-v1.0.Q5_K_M.gguf"},
			},
			quant:  "Q4_K_M",
			repoID: "TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF",
			want:   "tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf",
		},
		{
			name: "case insensitive quant match",
			siblings: []hfRepoFile{
				{Filename: "model-q4_k_m.gguf"},
			},
			quant:  "Q4_K_M",
			repoID: "user/model-GGUF",
			want:   "model-q4_k_m.gguf",
		},
		{
			name: "no match falls back to heuristic",
			siblings: []hfRepoFile{
				{Filename: "something-else.gguf"},
				{Filename: "README.md"},
			},
			quant:  "Q4_K_M",
			repoID: "user/model-GGUF",
			want:   "model-Q4_K_M.gguf", // heuristic fallback
		},
		{
			name: "multiple matches picks first boundary match",
			siblings: []hfRepoFile{
				{Filename: "modelQ4_K_Mstuff.gguf"},  // no boundary (alphanumeric before)
				{Filename: "model.Q4_K_M.gguf"},
			},
			quant:  "Q4_K_M",
			repoID: "user/model-GGUF",
			want:   "model.Q4_K_M.gguf",
		},
		{
			name: "empty siblings falls back",
			siblings: []hfRepoFile{},
			quant:  "Q4_K_M",
			repoID: "user/model-GGUF",
			want:   "model-Q4_K_M.gguf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchGGUFFile(tt.siblings, tt.quant, tt.repoID)
			if got != tt.want {
				t.Errorf("matchGGUFFile() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAcquireLock_Basic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	shelfRoot := t.TempDir()

	release, err := acquireLock(shelfRoot, "user/model", "Q4_K_M")
	if err != nil {
		t.Fatal(err)
	}

	// Lock file should exist.
	path := lockPath(shelfRoot, "user/model", "Q4_K_M")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected lock file to exist: %v", err)
	}

	release()

	// Lock file should be removed after release.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected lock file to be removed after release")
	}
}

func TestAcquireLock_Concurrent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	shelfRoot := t.TempDir()

	// Simulate concurrent access: two goroutines try to acquire the same lock.
	var mu sync.Mutex
	var order []int

	var wg sync.WaitGroup
	wg.Add(2)

	for i := 0; i < 2; i++ {
		go func(id int) {
			defer wg.Done()
			release, err := acquireLock(shelfRoot, "user/model", "Q4_K_M")
			if err != nil {
				t.Errorf("goroutine %d: %v", id, err)
				return
			}
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			release()
		}(i)
	}
	wg.Wait()

	// Both should have succeeded (serialized).
	if len(order) != 2 {
		t.Errorf("expected 2 successful acquisitions, got %d", len(order))
	}
}

func TestLockPath_Sanitization(t *testing.T) {
	root := "/tmp/shelf"
	got := lockPath(root, "TheBloke/TinyLlama-1.1B-GGUF", "Q4_K_M")
	want := filepath.Join(root, ".locks", "TheBloke--TinyLlama-1.1B-GGUF-Q4_K_M.lock")
	if got != want {
		t.Errorf("lockPath() = %q, want %q", got, want)
	}
}

func TestLookupGGUFFilename_APISuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Mock HF API returning repo siblings.
	repoInfo := struct {
		Siblings []hfRepoFile `json:"siblings"`
	}{
		Siblings: []hfRepoFile{
			{Filename: "README.md"},
			{Filename: "tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf"},
			{Filename: "tinyllama-1.1b-chat-v1.0.Q5_K_M.gguf"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(repoInfo)
	}))
	defer srv.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	got := LookupGGUFFilename("TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF", "Q4_K_M")
	want := "tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf"
	if got != want {
		t.Errorf("LookupGGUFFilename() = %q, want %q", got, want)
	}
}

func TestLookupGGUFFilename_APIFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Mock server returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	// Should fall back to heuristic.
	got := LookupGGUFFilename("TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF", "Q4_K_M")
	want := "TinyLlama-1.1B-Chat-v1.0-Q4_K_M.gguf"
	if got != want {
		t.Errorf("LookupGGUFFilename() = %q, want %q", got, want)
	}
}
