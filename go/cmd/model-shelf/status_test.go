package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestCmdStatus_SingleJob_JSON_TransferFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	job := daemon.Job{
		ID:              "transfer123456ab",
		Type:            daemon.JobTypeTransfer,
		RepoID:          "Qwen/Qwen3-14B-GGUF",
		Format:          "gguf",
		Quant:           "Q4_K_M",
		Target:          "gpu-box-1",
		Source:          "nas-store",
		Status:          daemon.JobTransferring,
		BytesDownloaded: 4_160_000_000,
		BytesTotal:      8_320_000_000,
		CreatedAt:       now.Add(-5 * time.Minute),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") == "transfer123456ab" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(job)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdStatus([]string{"transfer123456ab", "--json"})

	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("expected valid JSON, got: %s (parse error: %v)", out, err)
	}
	if result["type"] != "transfer" {
		t.Errorf("expected type=transfer, got %v", result["type"])
	}
	if result["source"] != "nas-store" {
		t.Errorf("expected source=nas-store, got %v", result["source"])
	}
	if result["job_id"] != "transfer123456ab" {
		t.Errorf("expected job_id=transfer123456ab, got %v", result["job_id"])
	}
}

func TestCmdStatus_AllJobs_JSON_IncludesType(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	jobs := []daemon.Job{
		{
			ID:        "download789",
			Type:      daemon.JobTypeDownload,
			RepoID:    "mlx-community/Qwen3-14B-mlx",
			Format:    "mlx",
			Target:    "ocilab1",
			Status:    daemon.JobDownloading,
			CreatedAt: now,
		},
		{
			ID:        "transfer789",
			Type:      daemon.JobTypeTransfer,
			RepoID:    "Qwen/Qwen3-14B-GGUF",
			Format:    "gguf",
			Quant:     "Q4_K_M",
			Target:    "mini2",
			Source:    "mini1",
			Status:    daemon.JobTransferring,
			CreatedAt: now,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdStatus([]string{"--json"})

	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	var result []map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("expected valid JSON array, got: %s (parse error: %v)", out, err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(result))
	}
	if result[0]["type"] != "download" {
		t.Errorf("expected first job type=download, got %v", result[0]["type"])
	}
	if result[1]["type"] != "transfer" {
		t.Errorf("expected second job type=transfer, got %v", result[1]["type"])
	}
	if result[1]["source"] != "mini1" {
		t.Errorf("expected second job source=mini1, got %v", result[1]["source"])
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

	var gotMeshParam atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mesh") == "true" {
			gotMeshParam.Store(true)
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
	if !gotMeshParam.Load() {
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

func TestCmdStatus_DefaultMesh_ControllerRole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	jobs := []daemon.Job{
		{
			ID:        "remote789",
			RepoID:    "Qwen/Qwen3-0.6B-GGUF",
			Format:    "gguf",
			Quant:     "Q8_0",
			Target:    "mini2",
			Status:    daemon.JobDownloading,
			CreatedAt: now.Add(-1 * time.Minute),
		},
	}

	var gotMeshParam atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mesh") == "true" {
			gotMeshParam.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}))
	defer server.Close()

	// writeMeshConfig writes roles=["controller"], so mesh should be default.
	writeMeshConfig(t, home, server)

	code := cmdStatus(nil) // no --mesh flag
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !gotMeshParam.Load() {
		t.Fatal("expected ?mesh=true to be sent by default for controller node")
	}
}

func TestCmdStatus_DefaultMesh_WithSeeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	jobs := []daemon.Job{}

	var gotMeshParam atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mesh") == "true" {
			gotMeshParam.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}))
	defer server.Close()

	// Write config with seeds but store role (not controller).
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	configDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(configDir, 0o755)
	config := fmt.Sprintf("name = \"test\"\nport = %d\nroles = [\"store\"]\nshelf_root = \"/tmp/shelf\"\nseeds = [\"peer1:8844\"]\n", port)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(config), 0o644)

	code := cmdStatus(nil)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !gotMeshParam.Load() {
		t.Fatal("expected ?mesh=true to be sent by default when seeds are configured")
	}
}

func TestCmdStatus_LocalFlag_OverridesDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	jobs := []daemon.Job{}

	var gotMeshParam atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mesh") == "true" {
			gotMeshParam.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}))
	defer server.Close()

	// writeMeshConfig writes roles=["controller"], so mesh would be default.
	writeMeshConfig(t, home, server)

	code := cmdStatus([]string{"--local"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if gotMeshParam.Load() {
		t.Fatal("expected ?mesh=true NOT to be sent when --local is specified")
	}
}

func TestCmdStatus_MeshAndLocal_MutuallyExclusive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]daemon.Job{})
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	code := cmdStatus([]string{"--mesh", "--local"})
	if code != 1 {
		t.Fatalf("expected exit code 1 when both --mesh and --local are given, got %d", code)
	}
}

func TestCmdStatus_NoMeshDefault_StoreOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	jobs := []daemon.Job{}

	var gotMeshParam atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mesh") == "true" {
			gotMeshParam.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}))
	defer server.Close()

	// Write config with store role only, no seeds.
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	configDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(configDir, 0o755)
	config := fmt.Sprintf("name = \"test\"\nport = %d\nroles = [\"store\"]\nshelf_root = \"/tmp/shelf\"\n", port)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(config), 0o644)

	code := cmdStatus(nil)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if gotMeshParam.Load() {
		t.Fatal("expected ?mesh=true NOT to be sent for store-only node without seeds")
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}

// TestCmdStatus_SingleJob_JSON_ErrorFieldAlwaysPresent verifies that the "error"
// field is always present in status --json output even when the job succeeded (#213).
func TestCmdStatus_SingleJob_JSON_ErrorFieldAlwaysPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	done := now.Add(-5 * time.Minute)
	job := daemon.Job{
		ID:              "completed123abc",
		Type:            daemon.JobTypeDownload,
		RepoID:          "ggml-org/Qwen3-0.6B-GGUF",
		Format:          "gguf",
		Quant:           "Q4_0",
		Target:          "mini2",
		Status:          daemon.JobCompleted,
		BytesDownloaded: 428_970_080,
		BytesTotal:      428_970_080,
		CreatedAt:       now.Add(-10 * time.Minute),
		DoneAt:          &done,
		// Error intentionally empty (success case).
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(job)
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdStatus([]string{"completed123abc", "--json"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (output: %s)", code, out)
	}

	// Unmarshal as raw map to verify key presence regardless of value.
	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("expected valid JSON, got: %s (parse error: %v)", out, err)
	}

	if _, ok := result["error"]; !ok {
		t.Errorf("expected 'error' key always present in JSON output, but it was absent; output: %s", out)
	}
	if result["error"] != "" {
		t.Errorf("expected error=\"\" for successful job, got %q", result["error"])
	}
}

// TestCmdStatus_AllJobs_JSON_ErrorFieldAlwaysPresent verifies that all jobs in
// the array output include the "error" field (#213).
func TestCmdStatus_AllJobs_JSON_ErrorFieldAlwaysPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	done := now.Add(-5 * time.Minute)
	jobs := []daemon.Job{
		{
			ID:        "success123",
			Type:      daemon.JobTypeDownload,
			RepoID:    "org/model-a",
			Format:    "gguf",
			Target:    "node1",
			Status:    daemon.JobCompleted,
			CreatedAt: now.Add(-10 * time.Minute),
			DoneAt:    &done,
		},
		{
			ID:        "failed456",
			Type:      daemon.JobTypeDownload,
			RepoID:    "org/model-b",
			Format:    "gguf",
			Target:    "node2",
			Status:    daemon.JobFailed,
			Error:     "connection refused",
			CreatedAt: now.Add(-8 * time.Minute),
			DoneAt:    &done,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}))
	defer server.Close()

	writeMeshConfig(t, home, server)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdStatus([]string{"--json", "--local"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (output: %s)", code, out)
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("expected valid JSON array, got: %s (parse error: %v)", out, err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %s", len(results), out)
	}

	// Both jobs must have the "error" key.
	for i, result := range results {
		if _, ok := result["error"]; !ok {
			t.Errorf("job %d: expected 'error' key present, but it was absent; job: %v", i, result)
		}
	}
	// Success job: error must be empty string.
	if results[0]["error"] != "" {
		t.Errorf("success job: expected error=\"\", got %q", results[0]["error"])
	}
	// Failed job: error must be non-empty.
	if results[1]["error"] == "" {
		t.Errorf("failed job: expected non-empty error, got empty string")
	}
}
