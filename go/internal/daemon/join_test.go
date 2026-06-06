package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestJoinEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "controller-node",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: t.TempDir(),
		MeshKey:   "test-mesh-key",
	}
	d := New(cfg)

	joinReq := JoinRequest{
		Name:    "store-node",
		Address: "192.168.1.10",
		Port:    8844,
		Roles:   []string{"store"},
	}
	body, _ := json.Marshal(joinReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/join", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-mesh-key")
	w := httptest.NewRecorder()

	// Use the full handler with auth middleware.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/join", d.handleJoin)
	handler := d.authMiddleware(mux)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp JoinResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !resp.OK {
		t.Error("expected OK=true")
	}

	// Should return the controller node in the nodes list (bootstrap state).
	if len(resp.Nodes) != 1 {
		t.Fatalf("expected 1 node in response, got %d", len(resp.Nodes))
	}
	if resp.Nodes[0].Name != "controller-node" {
		t.Errorf("expected node name 'controller-node', got %q", resp.Nodes[0].Name)
	}

	// Verify the new node was registered in gossip state.
	gossipNodes := d.gossip.Nodes()
	if len(gossipNodes) != 2 {
		t.Fatalf("expected 2 nodes in gossip after join, got %d", len(gossipNodes))
	}
	// Find the store-node in gossip state.
	var found bool
	for _, n := range gossipNodes {
		if n.Name == "store-node" {
			found = true
			if n.Address != "192.168.1.10" {
				t.Errorf("expected address '192.168.1.10', got %q", n.Address)
			}
			break
		}
	}
	if !found {
		t.Errorf("store-node not found in gossip state: %+v", gossipNodes)
	}
}

func TestJoinEndpoint_Unauthorized(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:    "controller-node",
		Port:    8844,
		Roles:   []string{"controller"},
		MeshKey: "correct-key",
	}
	d := New(cfg)

	joinReq := JoinRequest{Name: "bad-node", Address: "10.0.0.1", Port: 8844, Roles: []string{"store"}}
	body, _ := json.Marshal(joinReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/join", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-key")
	w := httptest.NewRecorder()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/join", d.handleJoin)
	handler := d.authMiddleware(mux)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestJoinEndpoint_MethodNotAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/join", nil)
	w := httptest.NewRecorder()
	d.handleJoin(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestJoinEndpoint_InvalidBody(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/join", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	d.handleJoin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestJoinEndpoint_MissingFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}}
	d := New(cfg)

	joinReq := JoinRequest{Name: "", Port: 0}
	body, _ := json.Marshal(joinReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/join", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.handleJoin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestJoinEndpoint_DiskMetricsAndLastSeen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "controller-node",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: t.TempDir(),
		MeshKey:   "test-key",
	}
	d := New(cfg)

	joinReq := JoinRequest{
		Name:        "store-node",
		Address:     "192.168.1.10",
		Port:        8844,
		Roles:       []string{"store"},
		DiskFreeGB:  750.5,
		DiskTotalGB: 1000.0,
	}
	body, _ := json.Marshal(joinReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/join", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/join", d.handleJoin)
	handler := d.authMiddleware(mux)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the joined node has disk metrics and last_seen populated.
	gossipNodes := d.gossip.Nodes()
	var storeNode *MeshNode
	for i := range gossipNodes {
		if gossipNodes[i].Name == "store-node" {
			storeNode = &gossipNodes[i]
			break
		}
	}
	if storeNode == nil {
		t.Fatal("store-node not found in gossip state")
	}
	if storeNode.DiskFreeGB != 750.5 {
		t.Errorf("expected DiskFreeGB=750.5, got %f", storeNode.DiskFreeGB)
	}
	if storeNode.DiskTotalGB != 1000.0 {
		t.Errorf("expected DiskTotalGB=1000.0, got %f", storeNode.DiskTotalGB)
	}
	if storeNode.LastSeen == nil {
		t.Error("expected LastSeen to be set immediately after join")
	}
}
