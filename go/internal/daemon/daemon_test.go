package daemon

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRun_PortConflict(t *testing.T) {
	// Pre-bind a port so the daemon's Run() fails on the same port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Extract the port number.
	port := ln.Addr().(*net.TCPAddr).Port

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      port,
		Roles:     []string{"store"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	err = d.Run()
	if err == nil {
		t.Fatal("expected error when port is already in use")
	}
	if !strings.Contains(err.Error(), "address already in use") {
		t.Errorf("unexpected error: %v", err)
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

func TestHealthEndpoint_WithGPU(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "gpu-node",
		Port:      8844,
		Roles:     []string{"executor"},
		ShelfRoot: t.TempDir(),
		GPU: &meshconfig.GPUConfig{
			Name:        "NVIDIA A100-SXM4-80GB",
			VRAMTotalGB: 80.0,
		},
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
	if resp.GPU == nil {
		t.Fatal("expected gpu field to be non-nil")
	}
	if resp.GPU.Name != "NVIDIA A100-SXM4-80GB" {
		t.Errorf("gpu name: got %q, want %q", resp.GPU.Name, "NVIDIA A100-SXM4-80GB")
	}
	if resp.GPU.VRAMTotalGB != 80.0 {
		t.Errorf("gpu vram_total_gb: got %f, want 80.0", resp.GPU.VRAMTotalGB)
	}
	if resp.GPU.VRAMAvailableGB != 80.0 {
		t.Errorf("gpu vram_available_gb: got %f, want 80.0", resp.GPU.VRAMAvailableGB)
	}
}

func TestHealthEndpoint_NoGPU(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "store-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: t.TempDir(),
		// No GPU config — and no nvidia-smi available in test env.
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	w := httptest.NewRecorder()
	d.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Parse raw JSON to verify gpu is null.
	var raw map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	gpuVal, exists := raw["gpu"]
	if !exists {
		t.Fatal("expected 'gpu' key in response")
	}
	if gpuVal != nil {
		t.Errorf("expected gpu to be null on node without GPU, got %v", gpuVal)
	}
}
