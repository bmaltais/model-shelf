package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexziskind1/model-shelf/internal/daemon"
)

func TestCmdStatus_AllJobs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	jobs := []daemon.Job{
		{
			ID:        "abc123def456",
			RepoID:    "mlx-community/Qwen3-14B-mlx",
			Format:    "mlx",
			Target:    "ocilab1",
			Status:    daemon.JobDownloading,
			CreatedAt: now.Add(-5 * time.Minute),
		},
		{
			ID:              "completed123456",
			RepoID:          "Qwen/Qwen3-14B-GGUF",
			Format:          "gguf",
			Quant:           "Q4_K_M",
			Target:          "ocilab2",
			Status:          daemon.JobCompleted,
			BytesDownloaded: 8_000_000_000,
			BytesTotal:      8_000_000_000,
			CreatedAt:       now.Add(-1 * time.Hour),
			DoneAt:          timePtr(now.Add(-30 * time.Minute)),
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	code := cmdStatus(nil)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
}

func TestCmdStatus_AllJobs_JSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	jobs := []daemon.Job{
		{
			ID:        "abc123def456",
			RepoID:    "mlx-community/Qwen3-14B-mlx",
			Format:    "mlx",
			Target:    "ocilab1",
			Status:    daemon.JobDownloading,
			CreatedAt: now,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	code := cmdStatus([]string{"--json"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
}

func TestCmdStatus_SingleJob(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	job := daemon.Job{
		ID:              "abc123def456abcd",
		RepoID:          "mlx-community/Qwen3-14B-mlx",
		Format:          "mlx",
		Target:          "ocilab1",
		Status:          daemon.JobCompleted,
		BytesDownloaded: 5_000_000_000,
		BytesTotal:      5_000_000_000,
		CreatedAt:       now.Add(-10 * time.Minute),
		DoneAt:          timePtr(now.Add(-5 * time.Minute)),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") == "abc123def456abcd" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(job)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	code := cmdStatus([]string{"abc123def456abcd"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
}

func TestCmdStatus_SingleJob_NotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	code := cmdStatus([]string{"nonexistent"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
}

func TestCmdStatus_NoJobs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]daemon.Job{})
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	code := cmdStatus(nil)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
}

func TestCmdStatus_Help(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	code := cmdStatus([]string{"--help"})
	if code != 0 {
		t.Fatalf("expected exit code 0 for help, got %d", code)
	}
}

func TestCmdStatus_MeshFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	localJobs := []daemon.Job{
		{
			ID:        "local123",
			RepoID:    "mlx-community/Qwen3-14B-mlx",
			Format:    "mlx",
			Target:    "ocilab1",
			Status:    daemon.JobDownloading,
			CreatedAt: now.Add(-5 * time.Minute),
		},
	}

	var gotMeshParam bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mesh") == "true" {
			gotMeshParam = true
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(localJobs)
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	code := cmdStatus([]string{"--mesh"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !gotMeshParam {
		t.Fatal("expected ?mesh=true query parameter to be sent to daemon")
	}
}

func TestCmdStatus_MeshFlag_JSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	jobs := []daemon.Job{
		{
			ID:        "remote456",
			RepoID:    "Qwen/Qwen3-14B-GGUF",
			Format:    "gguf",
			Quant:     "Q4_K_M",
			Target:    "mini2",
			Status:    daemon.JobCompleted,
			CreatedAt: now.Add(-1 * time.Hour),
			DoneAt:    timePtr(now.Add(-30 * time.Minute)),
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	code := cmdStatus([]string{"--mesh", "--json"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
}

func TestCmdStatus_DaemonUnreachable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Write mesh config pointing to a port nobody is listening on.
	configDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(configDir, 0o755)
	config := "name = \"test\"\nport = 19999\nroles = [\"controller\"]\nshelf_root = \"/tmp/shelf\"\n"
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(config), 0o644)

	code := cmdStatus(nil)
	if code != 1 {
		t.Fatalf("expected exit code 1 when daemon unreachable, got %d", code)
	}
}

// writeMeshConfig writes a mesh config file pointing at the test server.
func writeMeshConfig(t *testing.T, home string, server *httptest.Server) {
	t.Helper()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	port, _ := strconv.Atoi(u.Port())
	host := u.Hostname()

	configDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(configDir, 0o755)

	// Write a config with the test server's port.
	// The status command reads port from config.
	config := strings.Join([]string{
		"name = \"test\"",
		"port = " + strconv.Itoa(port),
		"roles = [\"controller\"]",
		"shelf_root = \"/tmp/shelf\"",
	}, "\n") + "\n"
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(config), 0o644)

	// Override daemonAddr to use the test server's host:port.
	// Since daemonAddr() always uses 127.0.0.1, and httptest uses 127.0.0.1,
	// this works if the port matches.
	_ = host // httptest always uses 127.0.0.1
}

func timePtr(t time.Time) *time.Time {
	return &t
}
