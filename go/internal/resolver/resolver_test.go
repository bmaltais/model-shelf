package resolver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		repoID string
		want   string
	}{
		{"Qwen/Qwen3-14B-GGUF", "gguf"},
		{"bartowski/Qwen3-8B-GGUF", "gguf"},
		{"mlx-community/Qwen3-14B-4bit", "mlx"},
		{"Qwen/Qwen3-4B-MLX-4bit", "mlx"},
		{"lmstudio-community/X-MLX-4bit", "mlx"},
		{"Qwen/Qwen3-14B", "safetensors"},
		{"meta-llama/Llama-3.1-8B-Instruct", "safetensors"},
	}
	for _, tt := range tests {
		t.Run(tt.repoID, func(t *testing.T) {
			got := DetectFormat(tt.repoID)
			if got != tt.want {
				t.Errorf("DetectFormat(%q) = %q, want %q", tt.repoID, got, tt.want)
			}
		})
	}
}

func TestHFFilename(t *testing.T) {
	tests := []struct {
		repoID, quant, want string
	}{
		{"Qwen/Qwen3-14B-GGUF", "Q4_K_M", "Qwen3-14B-Q4_K_M.gguf"},
		{"meta-llama/Llama-3.1-8B-Instruct-GGUF", "Q5_K_M", "Llama-3.1-8B-Instruct-Q5_K_M.gguf"},
		{"bartowski/Qwen3-0.6B-Q4_K_M-GGUF", "Q4_K_M", "Qwen3-0.6B-Q4_K_M.gguf"},
	}
	for _, tt := range tests {
		t.Run(tt.repoID, func(t *testing.T) {
			got := HFFilename(tt.repoID, tt.quant)
			if got != tt.want {
				t.Errorf("HFFilename(%q, %q) = %q, want %q", tt.repoID, tt.quant, got, tt.want)
			}
		})
	}
}

func TestShelfPathGGUF(t *testing.T) {
	root := filepath.Join("test", "shelf")
	path, err := ShelfPathGGUF(root, "Qwen/Qwen3-14B-GGUF", "Q4_K_M")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "gguf", "Qwen", "Qwen3-14B-GGUF", "Qwen3-14B-Q4_K_M.gguf")
	if path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}

func TestShelfPathSnapshot(t *testing.T) {
	root := filepath.Join("test", "shelf")
	path, err := ShelfPathSnapshot(root, "mlx-community/Qwen3-14B-4bit", "mlx")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "mlx", "mlx-community", "Qwen3-14B-4bit")
	if path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}

func TestSplitRepoIDError(t *testing.T) {
	_, _, err := splitRepoID("no-slash")
	if err == nil {
		t.Error("expected error for repo_id without slash")
	}
}

func TestCleanupOnFailure(t *testing.T) {
	t.Run("removes new directory", func(t *testing.T) {
		tmp := t.TempDir()
		dir := filepath.Join(tmp, "publisher", "repo")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		cleanupOnFailure(dir, false)
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Error("expected directory to be removed")
		}
	})

	t.Run("preserves pre-existing directory", func(t *testing.T) {
		tmp := t.TempDir()
		dir := filepath.Join(tmp, "existing")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		cleanupOnFailure(dir, true)
		if _, err := os.Stat(dir); err != nil {
			t.Error("expected directory to still exist")
		}
	})
}

func TestDirExists(t *testing.T) {
	tmp := t.TempDir()
	if !dirExists(tmp) {
		t.Error("expected true for existing directory")
	}
	if dirExists(filepath.Join(tmp, "nonexistent")) {
		t.Error("expected false for nonexistent path")
	}
}

func TestResolveGGUF_404CleansUpDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Start a test server that always returns 404.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	// Override http.DefaultClient transport to redirect HF requests to our test server.
	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	shelfRoot := t.TempDir()
	// Create the shelf root with gguf subdir so CheckStorageAvailable passes.
	os.MkdirAll(filepath.Join(shelfRoot, "gguf"), 0o755)

	cfg := &Config{
		ShelfRoot:      shelfRoot,
		AllowDownloads: true,
	}

	_, err := ResolveModel(cfg, "TestPublisher/TestModel-GGUF", "gguf", "Q4_K_M")
	if err == nil {
		t.Fatal("expected download error, got nil")
	}

	// The publisher/repo directory should NOT exist after the failure.
	ghostDir := filepath.Join(shelfRoot, "gguf", "TestPublisher", "TestModel-GGUF")
	if _, statErr := os.Stat(ghostDir); !os.IsNotExist(statErr) {
		t.Errorf("expected ghost directory %q to be cleaned up, but it still exists", ghostDir)
	}
}

func TestResolveSnapshot_404CleansUpDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Start a test server that always returns 404.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	shelfRoot := t.TempDir()
	os.MkdirAll(filepath.Join(shelfRoot, "safetensors"), 0o755)

	cfg := &Config{
		ShelfRoot:      shelfRoot,
		AllowDownloads: true,
	}

	_, err := ResolveModel(cfg, "TestPublisher/TestModel", "safetensors", "")
	if err == nil {
		t.Fatal("expected download error, got nil")
	}

	// The model directory should NOT exist after the failure.
	ghostDir := filepath.Join(shelfRoot, "safetensors", "TestPublisher", "TestModel")
	if _, statErr := os.Stat(ghostDir); !os.IsNotExist(statErr) {
		t.Errorf("expected ghost directory %q to be cleaned up, but it still exists", ghostDir)
	}
}

// rewriteTransport redirects all requests to a local test server.
type rewriteTransport struct {
	target    string
	transport http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.target[len("http://"):]
	return t.transport.RoundTrip(req)
}

func TestInitShelf_FileBlocksPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()

	// Create a regular file where a directory is needed.
	blocker := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blocker, []byte("I'm a file"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{ShelfRoot: filepath.Join(blocker, "models")}
	_, err := InitShelf(cfg)
	if err == nil {
		t.Fatal("expected error when path component is a file")
	}
	if got := err.Error(); !strings.Contains(got, "exists but is not a directory") {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestCheckPathAncestors_OK(t *testing.T) {
	tmp := t.TempDir()
	// Path that can be created — no blockers.
	err := checkPathAncestors(filepath.Join(tmp, "a", "b", "c"))
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestExtractRepoID(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://huggingface.co/owner/repo/resolve/main/file.gguf", "owner/repo"},
		{"https://huggingface.co/a/b/resolve/main/deep/file.bin", "a/b"},
		{"https://example.com/foo", ""},
	}
	for _, tt := range tests {
		got := extractRepoID(tt.url)
		if got != tt.want {
			t.Errorf("extractRepoID(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestClarify401_RepoNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Server returns 404 for the API check (repo doesn't exist).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	err := clarify401("https://huggingface.co/fake-user/fake-repo/resolve/main/file.gguf")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "not found on Hugging Face") {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestClarify401_RepoRequiresAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Server returns 200 for the API check (repo exists but is private).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	err := clarify401("https://huggingface.co/private-user/private-repo/resolve/main/file.gguf")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "requires authentication") {
		t.Errorf("unexpected error: %s", got)
	}
}


