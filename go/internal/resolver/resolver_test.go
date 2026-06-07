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

func TestSplitRepoID_TooManySlashes(t *testing.T) {
	_, _, err := splitRepoID("too/many/slashes")
	if err == nil {
		t.Fatal("expected error for repo_id with multiple slashes")
	}
	if !strings.Contains(err.Error(), "publisher/repo") {
		t.Errorf("error should mention expected format, got: %s", err.Error())
	}
}

func TestSplitRepoID_Valid(t *testing.T) {
	pub, repo, err := splitRepoID("Qwen/Qwen3-14B")
	if err != nil {
		t.Fatal(err)
	}
	if pub != "Qwen" || repo != "Qwen3-14B" {
		t.Errorf("got %q/%q, want Qwen/Qwen3-14B", pub, repo)
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
	t.Setenv("HF_TOKEN", "test-token")

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

func TestClarify401_NoToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HF_TOKEN", "")

	// Server returns 401 for the API check (no token, ambiguous).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	err := clarify401("https://huggingface.co/fake-user/fake-repo/resolve/main/file.gguf")
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if !strings.Contains(got, "not found or requires authentication") {
		t.Errorf("unexpected error: %s", got)
	}
	if !strings.Contains(got, "set HF_TOKEN for gated repos") {
		t.Errorf("expected hint about HF_TOKEN for gated repos, got: %s", got)
	}
}

func TestClarify401_UnexpectedStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Server returns 503 (unexpected) — should fall back to generic 401.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	err := clarify401("https://huggingface.co/some-user/some-repo/resolve/main/file.gguf")
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if !strings.Contains(got, "HTTP 401") || !strings.Contains(got, "probe returned 503") {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestLooksLikeModelDir_RequiresWeightFile(t *testing.T) {
	// Directory with only config.json should NOT be detected as a valid model.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644)

	if looksLikeModelDir(dir) {
		t.Error("expected false for directory with only config.json (no weight files)")
	}
}

func TestLooksLikeModelDir_WithSafetensors(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("weights"), 0o644)

	if !looksLikeModelDir(dir) {
		t.Error("expected true for directory with config.json and .safetensors file")
	}
}

func TestLooksLikeModelDir_WithBinFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "pytorch_model.bin"), []byte("weights"), 0o644)

	if !looksLikeModelDir(dir) {
		t.Error("expected true for directory with config.json and .bin file")
	}
}

func TestLooksLikeModelDir_WithNpyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "weights.npz"), []byte("weights"), 0o644)

	if !looksLikeModelDir(dir) {
		t.Error("expected true for directory with config.json and .npz file")
	}
}

func TestLooksLikeModelDir_NoConfigJson(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("weights"), 0o644)

	if looksLikeModelDir(dir) {
		t.Error("expected false for directory without config.json")
	}
}

func TestLooksLikeModelDir_NonExistentPath(t *testing.T) {
	if looksLikeModelDir("/nonexistent/path/xyz") {
		t.Error("expected false for non-existent path")
	}
}

func TestGGUFQuantLabel(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"Qwen3-0.6B-Q4_K_M.gguf", "Q4_K_M"},
		{"Qwen3-0.6B-f16.gguf", "f16"},
		{"Qwen3-0.6B-Q8_0.gguf", "Q8_0"},
		{"model.Q4_0.gguf", "Q4_0"},
		{"model.gguf", "model"},
	}
	for _, tc := range tests {
		got := ggufQuantLabel(tc.filename)
		if got != tc.want {
			t.Errorf("ggufQuantLabel(%q) = %q, want %q", tc.filename, got, tc.want)
		}
	}
}

func TestGGUFSearchHint(t *testing.T) {
	tests := []struct {
		repoID string
		want   string
	}{
		{"ggml-org/Qwen3-0.6B-GGUF", "Qwen3 0.6B"},
		{"unsloth/Qwen3-1.7B-GGUF", "Qwen3 1.7B"},
		{"TheBloke/Mistral-7B-v0.1-GGUF", "Mistral 7B v0.1"},
	}
	for _, tc := range tests {
		got := ggufSearchHint(tc.repoID)
		if got != tc.want {
			t.Errorf("ggufSearchHint(%q) = %q, want %q", tc.repoID, got, tc.want)
		}
	}
}

func TestClarifyMissingGGUFQuant_ListsAvailableQuants(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Test server returning a HF API response with known .gguf siblings.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"siblings":[
			{"rfilename":"Qwen3-0.6B-Q4_0.gguf"},
			{"rfilename":"Qwen3-0.6B-Q8_0.gguf"},
			{"rfilename":"Qwen3-0.6B-f16.gguf"},
			{"rfilename":"README.md"}
		]}`))
	}))
	defer srv.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	err := clarifyMissingGGUFQuant("ggml-org/Qwen3-0.6B-GGUF", "Q4_K_M")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"Q4_K_M"`) {
		t.Errorf("expected quant name in error, got: %s", msg)
	}
	if !strings.Contains(msg, "Q4_0") || !strings.Contains(msg, "Q8_0") || !strings.Contains(msg, "f16") {
		t.Errorf("expected available quants listed, got: %s", msg)
	}
	if !strings.Contains(msg, "model-shelf find") {
		t.Errorf("expected find hint in error, got: %s", msg)
	}
}

func TestClarifyMissingGGUFQuant_APIUnavailable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Test server returning 503 — clarify function should return nil (fall back to original error).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	err := clarifyMissingGGUFQuant("ggml-org/Qwen3-0.6B-GGUF", "Q4_K_M")
	if err != nil {
		t.Errorf("expected nil when API unavailable, got: %v", err)
	}
}

func TestResolveGGUF_404ShowsFriendlyQuantError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// First request (LookupGGUFFilename HF API call) returns siblings without Q4_K_M.
	// Second request (download) returns 404.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/models/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"siblings":[
				{"rfilename":"Qwen3-0.6B-Q4_0.gguf"},
				{"rfilename":"Qwen3-0.6B-Q8_0.gguf"}
			]}`))
		} else {
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: srv.URL, transport: origTransport}
	defer func() { http.DefaultTransport = origTransport }()

	shelfRoot := t.TempDir()
	os.MkdirAll(filepath.Join(shelfRoot, "gguf"), 0o755)
	cfg := &Config{ShelfRoot: shelfRoot, AllowDownloads: true}

	_, err := ResolveModel(cfg, "ggml-org/Qwen3-0.6B-GGUF", "gguf", "Q4_K_M")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"Q4_K_M"`) || !strings.Contains(msg, "not found") {
		t.Errorf("expected friendly quant-not-found message, got: %s", msg)
	}
}
