package selfupgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetHTTPClient restores the package-level httpClient after a test.
func resetHTTPClient(t *testing.T, orig *http.Client) {
	t.Helper()
	t.Cleanup(func() { httpClient = orig })
}

func TestVerifyChecksum_match(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary")
	content := []byte("hello model-shelf")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	expected := hex.EncodeToString(h[:])

	if err := VerifyChecksum(path, expected); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestVerifyChecksum_mismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary")
	if err := os.WriteFile(path, []byte("real content"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := VerifyChecksum(path, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestVerifyChecksum_missingFile(t *testing.T) {
	err := VerifyChecksum("/nonexistent/path/binary", "abc123")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestFetchLatestVersion_success(t *testing.T) {
	orig := httpClient
	resetHTTPClient(t, orig)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v1.2.3","name":"Release 1.2.3"}`)
	}))
	defer srv.Close()

	// Patch the global API URL via the httpClient transport.
	httpClient = srv.Client()

	// We can't easily redirect the hardcoded URL, so we test FetchLatestVersion
	// indirectly by calling the underlying JSON parsing logic via a helper.
	// Instead, point httpClient at a custom transport that redirects to our server.
	httpClient = &http.Client{
		Transport: &redirectTransport{base: srv.URL},
	}

	v, err := FetchLatestVersion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "1.2.3" {
		t.Errorf("expected version 1.2.3, got %q", v)
	}
}

func TestFetchLatestVersion_stripsLeadingV(t *testing.T) {
	orig := httpClient
	resetHTTPClient(t, orig)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v0.5.9"}`)
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{base: srv.URL},
	}

	v, err := FetchLatestVersion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "0.5.9" {
		t.Errorf("expected 0.5.9, got %q", v)
	}
}

func TestFetchLatestVersion_httpError(t *testing.T) {
	orig := httpClient
	resetHTTPClient(t, orig)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{base: srv.URL},
	}

	_, err := FetchLatestVersion()
	if err == nil {
		t.Fatal("expected error for non-200 response, got nil")
	}
}

func TestFetchChecksums_parsing(t *testing.T) {
	orig := httpClient
	resetHTTPClient(t, orig)

	fixture := "abc123def456  model-shelf-linux-amd64\n" +
		"deadbeef0000  model-shelf-darwin-arm64\n" +
		"cafebabe1111  model-shelf-windows-amd64.exe\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, fixture)
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{base: srv.URL},
	}

	sums, err := FetchChecksums("1.2.3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cases := []struct {
		file string
		sha  string
	}{
		{"model-shelf-linux-amd64", "abc123def456"},
		{"model-shelf-darwin-arm64", "deadbeef0000"},
		{"model-shelf-windows-amd64.exe", "cafebabe1111"},
	}
	for _, c := range cases {
		got, ok := sums[c.file]
		if !ok {
			t.Errorf("missing entry for %s", c.file)
			continue
		}
		if got != c.sha {
			t.Errorf("entry %s: expected %q, got %q", c.file, c.sha, got)
		}
	}
}

func TestFetchChecksums_emptyLines(t *testing.T) {
	orig := httpClient
	resetHTTPClient(t, orig)

	fixture := "\nabc123  model-shelf-linux-amd64\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, fixture)
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{base: srv.URL},
	}

	sums, err := FetchChecksums("1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sums) != 1 {
		t.Errorf("expected 1 entry, got %d", len(sums))
	}
}

func TestRun_alreadyCurrent(t *testing.T) {
	var out strings.Builder
	// No network calls should be made — same version, no --force.
	err := Run("1.2.3", "1.2.3", false, false, &out, &out, strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("expected 'nothing to do' message, got: %q", out.String())
	}
}

func TestRun_alreadyCurrent_withVPrefix(t *testing.T) {
	var out strings.Builder
	err := Run("v1.2.3", "v1.2.3", false, false, &out, &out, strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("expected 'nothing to do' message, got: %q", out.String())
	}
}

func TestRun_alreadyCurrent_force(t *testing.T) {
	orig := httpClient
	resetHTTPClient(t, orig)

	// When --force is set even on same version, it should proceed and fetch checksums.
	// We simulate a server that returns an error to confirm the code path continues.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "simulated error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{base: srv.URL},
	}

	var out strings.Builder
	err := Run("1.2.3", "1.2.3", true /*yes*/, true /*force*/, &out, &out, strings.NewReader(""))
	// We expect it to fail at the checksums download step, not the "already current" exit.
	if err == nil {
		t.Fatal("expected an error (no real server), got nil")
	}
	if strings.Contains(out.String(), "nothing to do") {
		t.Error("should not have exited early with 'nothing to do' when --force is set")
	}
}

func TestRun_confirmationDenied(t *testing.T) {
	orig := httpClient
	resetHTTPClient(t, orig)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v2.0.0"}`)
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{base: srv.URL},
	}

	var out strings.Builder
	// Simulate the user typing "n" at the prompt.
	err := Run("2.0.0", "1.0.0", false /*yes*/, false /*force*/, &out, &out, strings.NewReader("n\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("expected 'cancelled' message, got: %q", out.String())
	}
}

func TestBinaryName(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "model-shelf-linux-amd64"},
		{"darwin", "arm64", "model-shelf-darwin-arm64"},
		{"windows", "amd64", "model-shelf-windows-amd64.exe"},
	}
	for _, c := range cases {
		got := BinaryName(c.goos, c.goarch)
		if got != c.want {
			t.Errorf("BinaryName(%q, %q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "model-shelf")
	original := []byte("original binary content")
	if err := os.WriteFile(binPath, original, 0o755); err != nil {
		t.Fatal(err)
	}

	newContent := []byte("new binary content")
	tmpPath := filepath.Join(dir, "model-shelf-upgrade-test.tmp")
	if err := os.WriteFile(tmpPath, newContent, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ReplaceBinary(tmpPath, binPath); err != nil {
		t.Fatalf("ReplaceBinary returned error: %v", err)
	}

	// Verify new binary has new content.
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newContent) {
		t.Errorf("binary content mismatch: got %q, want %q", got, newContent)
	}

	// Verify backup exists with original content.
	bak, err := os.ReadFile(binPath + ".bak")
	if err != nil {
		t.Fatalf("backup file not found: %v", err)
	}
	if string(bak) != string(original) {
		t.Errorf("backup content mismatch: got %q, want %q", bak, original)
	}

	// Verify permissions are preserved (executable).
	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("binary should be executable, mode = %v", info.Mode())
	}
}

// redirectTransport rewrites all request URLs to point to base (a test server URL).
// This allows package-level tests to intercept hardcoded URLs.
type redirectTransport struct {
	base string
	rt   http.RoundTripper
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request and rewrite the host.
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = "http"
	cloned.URL.Host = strings.TrimPrefix(rt.base, "http://")
	transport := rt.rt
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(cloned)
}
