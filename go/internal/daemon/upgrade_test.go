package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
	"github.com/alexziskind1/model-shelf/internal/selfupgrade"
)

// newUpgradeDaemon creates a minimal Daemon for upgrade handler tests.
func newUpgradeDaemon(version, meshKey string) *Daemon {
	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: "",
		Version:   version,
		MeshKey:   meshKey,
	}
	return New(cfg)
}

// stubFetchChecksums replaces fetchChecksums with the given map and restores on cleanup.
func stubFetchChecksums(t *testing.T, sums map[string]string, err error) {
	t.Helper()
	orig := fetchChecksums
	fetchChecksums = func(version string) (map[string]string, error) { return sums, err }
	t.Cleanup(func() { fetchChecksums = orig })
}

// stubDaemonUpgrade replaces daemonUpgrade with the given error and restores on cleanup.
func stubDaemonUpgrade(t *testing.T, err error) {
	t.Helper()
	orig := daemonUpgrade
	daemonUpgrade = func(version, expectedSHA string) error { return err }
	t.Cleanup(func() { daemonUpgrade = orig })
}

// currentPlatformChecksums returns a checksums map with an entry for the current
// platform binary — used to satisfy the sync validation step in handleUpgrade.
func currentPlatformChecksums(sha string) map[string]string {
	return map[string]string{
		selfupgrade.BinaryName("linux", "amd64"):   sha,
		selfupgrade.BinaryName("darwin", "arm64"):  sha,
		selfupgrade.BinaryName("darwin", "amd64"):  sha,
		selfupgrade.BinaryName("windows", "amd64"): sha,
	}
}

const fakeSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestHandleUpgrade_MethodNotAllowed(t *testing.T) {
	d := newUpgradeDaemon("0.5.0", "")
	req := httptest.NewRequest(http.MethodGet, "/v1/upgrade", nil)
	w := httptest.NewRecorder()
	d.handleUpgrade(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleUpgrade_MissingVersion(t *testing.T) {
	d := newUpgradeDaemon("0.5.0", "")
	req := httptest.NewRequest(http.MethodPost, "/v1/upgrade", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	d.handleUpgrade(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleUpgrade_InvalidBody(t *testing.T) {
	d := newUpgradeDaemon("0.5.0", "")
	req := httptest.NewRequest(http.MethodPost, "/v1/upgrade", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	d.handleUpgrade(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleUpgrade_AlreadyCurrent(t *testing.T) {
	d := newUpgradeDaemon("0.6.0", "")
	req := httptest.NewRequest(http.MethodPost, "/v1/upgrade", strings.NewReader(`{"version": "0.6.0"}`))
	w := httptest.NewRecorder()
	d.handleUpgrade(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp upgradeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Status != "already_current" {
		t.Errorf("expected already_current, got %q", resp.Status)
	}
}

func TestHandleUpgrade_AlreadyCurrent_VPrefix(t *testing.T) {
	d := newUpgradeDaemon("0.6.0", "")
	req := httptest.NewRequest(http.MethodPost, "/v1/upgrade", strings.NewReader(`{"version": "v0.6.0"}`))
	w := httptest.NewRecorder()
	d.handleUpgrade(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleUpgrade_Unauthorized(t *testing.T) {
	d := newUpgradeDaemon("0.5.0", "secretkey")

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/upgrade", d.handleUpgrade)
	handler := d.authMiddleware(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/upgrade", strings.NewReader(`{"version": "0.6.0"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleUpgrade_Accepted(t *testing.T) {
	stubFetchChecksums(t, currentPlatformChecksums(fakeSHA), nil)
	stubDaemonUpgrade(t, nil)

	d := newUpgradeDaemon("0.5.0", "")
	req := httptest.NewRequest(http.MethodPost, "/v1/upgrade", strings.NewReader(`{"version": "0.6.0"}`))
	w := httptest.NewRecorder()
	d.handleUpgrade(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp upgradeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Status != "accepted" {
		t.Errorf("expected accepted, got %q", resp.Status)
	}
	if resp.Version != "0.6.0" {
		t.Errorf("expected version 0.6.0, got %q", resp.Version)
	}
}

func TestHandleUpgrade_ChecksumFetchError(t *testing.T) {
	stubFetchChecksums(t, nil, fmt.Errorf("downloading checksums.txt: HTTP 404"))

	d := newUpgradeDaemon("0.5.0", "")
	req := httptest.NewRequest(http.MethodPost, "/v1/upgrade", strings.NewReader(`{"version": "0.6.0"}`))
	w := httptest.NewRecorder()
	d.handleUpgrade(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleUpgrade_PlatformNotInChecksums(t *testing.T) {
	// Return checksums that don't include the current platform.
	stubFetchChecksums(t, map[string]string{"model-shelf-otherplatform-amd64": fakeSHA}, nil)

	d := newUpgradeDaemon("0.5.0", "")
	req := httptest.NewRequest(http.MethodPost, "/v1/upgrade", strings.NewReader(`{"version": "0.6.0"}`))
	w := httptest.NewRecorder()
	d.handleUpgrade(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}
