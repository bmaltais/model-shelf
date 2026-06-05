package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestHealthEndpoint(t *testing.T) {
	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"controller", "store"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	w := httptest.NewRecorder()

	d.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Name != "test-node" {
		t.Errorf("expected name 'test-node', got %q", resp.Name)
	}
	if len(resp.Roles) != 2 || resp.Roles[0] != "controller" || resp.Roles[1] != "store" {
		t.Errorf("unexpected roles: %v", resp.Roles)
	}
	if resp.Port != 8844 {
		t.Errorf("expected port 8844, got %d", resp.Port)
	}
	if resp.UptimeSeconds < 0 {
		t.Errorf("uptime should be >= 0, got %f", resp.UptimeSeconds)
	}
	if resp.DiskTotalGB <= 0 {
		t.Errorf("disk_total_gb should be > 0, got %f", resp.DiskTotalGB)
	}
	if resp.DiskFreeGB <= 0 {
		t.Errorf("disk_free_gb should be > 0, got %f", resp.DiskFreeGB)
	}
}

func TestHealthMethodNotAllowed(t *testing.T) {
	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/health", nil)
	w := httptest.NewRecorder()

	d.handleHealth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestAuthMiddleware_NoKey(t *testing.T) {
	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}, MeshKey: ""}
	d := New(cfg)

	handler := d.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (no key configured), got %d", w.Code)
	}
}

func TestAuthMiddleware_ValidKey(t *testing.T) {
	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}, MeshKey: "secret123"}
	d := New(cfg)

	handler := d.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/nodes", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_InvalidKey(t *testing.T) {
	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}, MeshKey: "secret123"}
	d := New(cfg)

	handler := d.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/nodes", nil)
	req.Header.Set("Authorization", "Bearer wrongkey")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_HealthPublic(t *testing.T) {
	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}, MeshKey: "secret123"}
	d := New(cfg)

	handler := d.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Health endpoint should be accessible without auth.
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_MissingKey(t *testing.T) {
	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}, MeshKey: "secret123"}
	d := New(cfg)

	handler := d.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/nodes", nil)
	// No Authorization header.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
