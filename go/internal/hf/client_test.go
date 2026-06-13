package hf_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmaltais/model-shelf/internal/hf"
)

func TestDownloadFile_Success(t *testing.T) {
	const content = "fake gguf model content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(content))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "model.gguf")
	err := hf.DownloadFile(srv.URL+"/model.gguf", dest, hf.DownloadOptions{NoProgress: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != content {
		t.Errorf("content = %q, want %q", got, content)
	}
}

func TestDownloadFile_Resume(t *testing.T) {
	const content = "fake gguf model content"
	var receivedRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRange = r.Header.Get("Range")
		rangeStart := 5
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)-rangeStart))
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(content[rangeStart:]))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "model.gguf")
	partial := dest + ".partial"
	// Pre-write 5 bytes as if a previous download was interrupted.
	os.WriteFile(partial, []byte(content[:5]), 0o644)

	err := hf.DownloadFile(srv.URL+"/model.gguf", dest, hf.DownloadOptions{NoProgress: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(receivedRange, "bytes=5-") {
		t.Errorf("Range header = %q, want bytes=5-...", receivedRange)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != content {
		t.Errorf("content = %q, want %q", got, content)
	}
}

func TestDownloadFile_CleansUpPartialOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "model.gguf")
	err := hf.DownloadFile(srv.URL+"/model.gguf", dest, hf.DownloadOptions{NoProgress: true})
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if _, err2 := os.Stat(dest + ".partial"); !os.IsNotExist(err2) {
		t.Error(".partial file should be cleaned up on error")
	}
}

func TestLoadToken_EnvVar(t *testing.T) {
	t.Setenv("HF_TOKEN", "test-token-from-env")
	tok := hf.LoadToken()
	if tok != "test-token-from-env" {
		t.Errorf("token = %q, want test-token-from-env", tok)
	}
}
