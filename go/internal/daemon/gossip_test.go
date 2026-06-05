package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestNodesEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "controller",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/nodes", nil)
	w := httptest.NewRecorder()
	d.handleNodes(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var nodes []MeshNode
	if err := json.Unmarshal(w.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Name != "controller" {
		t.Errorf("expected node name 'controller', got %q", nodes[0].Name)
	}
	if nodes[0].Status != StatusOnline {
		t.Errorf("expected status online, got %q", nodes[0].Status)
	}
}

func TestNodesEndpoint_MethodNotAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/nodes", nil)
	w := httptest.NewRecorder()
	d.handleNodes(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestEventsEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "controller",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	// Send a join event.
	ev := Event{
		Type: EventJoin,
		Node: MeshNode{
			Name:    "store-1",
			Address: "10.0.0.2",
			Port:    8844,
			Roles:   []string{"store"},
			Status:  StatusOnline,
		},
		Timestamp: time.Now(),
	}
	body, _ := json.Marshal(ev)

	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.handleEvents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the node was added to gossip state.
	nodes := d.gossip.Nodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	found := false
	for _, n := range nodes {
		if n.Name == "store-1" {
			found = true
			if n.Address != "10.0.0.2" {
				t.Errorf("expected address 10.0.0.2, got %q", n.Address)
			}
		}
	}
	if !found {
		t.Error("store-1 not found in gossip nodes")
	}
}

func TestEventsEndpoint_LeaveEvent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "controller",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	// First add a node.
	d.gossip.ApplyEvent(Event{
		Type: EventJoin,
		Node: MeshNode{Name: "store-1", Address: "10.0.0.2", Port: 8844, Roles: []string{"store"}, Status: StatusOnline},
	})

	// Now send a leave event.
	ev := Event{
		Type:      EventLeave,
		Node:      MeshNode{Name: "store-1"},
		Timestamp: time.Now(),
	}
	body, _ := json.Marshal(ev)

	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.handleEvents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	nodes := d.gossip.Nodes()
	for _, n := range nodes {
		if n.Name == "store-1" {
			t.Error("store-1 should have been removed after leave event")
		}
	}
}

func TestEventsEndpoint_InvalidBody(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	d.handleEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestEventsEndpoint_UnknownEventType(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}}
	d := New(cfg)

	ev := Event{Type: "bogus", Node: MeshNode{Name: "x"}, Timestamp: time.Now()}
	body, _ := json.Marshal(ev)

	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.handleEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown event type, got %d", w.Code)
	}
}

func TestEventsEndpoint_MissingNodeName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}}
	d := New(cfg)

	ev := Event{Type: EventJoin, Node: MeshNode{Name: ""}, Timestamp: time.Now()}
	body, _ := json.Marshal(ev)

	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.handleEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing node name, got %d", w.Code)
	}
}

func TestEventsEndpoint_MethodNotAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{Name: "test", Port: 8844, Roles: []string{"store"}}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	w := httptest.NewRecorder()
	d.handleEvents(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestGossipStatePersistence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	selfNode := MeshNode{
		Name:    "node-1",
		Address: "10.0.0.1",
		Port:    8844,
		Roles:   []string{"controller"},
		Status:  StatusOnline,
	}

	g := NewGossip(selfNode, "key123", "", time.Now())

	// Add a peer node.
	g.ApplyEvent(Event{
		Type: EventJoin,
		Node: MeshNode{Name: "node-2", Address: "10.0.0.2", Port: 8844, Roles: []string{"store"}, Status: StatusOnline},
	})

	// Verify state file was created.
	statePath := filepath.Join(home, ".model-shelf", "state", "mesh.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	var persisted []MeshNode
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("failed to parse state file: %v", err)
	}

	if len(persisted) != 2 {
		t.Fatalf("expected 2 nodes in state file, got %d", len(persisted))
	}

	// Create a new gossip instance — it should load persisted state.
	g2 := NewGossip(selfNode, "key123", "", time.Now())
	nodes := g2.Nodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes after reload, got %d", len(nodes))
	}
}

func TestGossipNodeOfflineAfterMissedPolls(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	selfNode := MeshNode{
		Name:    "controller",
		Address: "10.0.0.1",
		Port:    8844,
		Roles:   []string{"controller"},
		Status:  StatusOnline,
	}

	g := NewGossip(selfNode, "", "", time.Now())

	// Start a mock server that always returns 503 (unhealthy).
	unhealthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unhealthyServer.Close()

	// Extract host and port from mock server.
	addr := unhealthyServer.Listener.Addr().String()
	host := "127.0.0.1"
	var port int
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			fmt.Sscanf(addr[i+1:], "%d", &port)
			break
		}
	}

	// Add a peer that returns unhealthy responses.
	g.ApplyEvent(Event{
		Type: EventJoin,
		Node: MeshNode{Name: "unreachable", Address: host, Port: port, Roles: []string{"store"}, Status: StatusOnline},
	})

	// Run pollPeers 3 times — node should go offline.
	for i := 0; i < 3; i++ {
		g.pollPeers()
	}

	nodes := g.Nodes()
	for _, n := range nodes {
		if n.Name == "unreachable" {
			if n.Status != StatusOffline {
				t.Errorf("expected node to be offline after 3 missed polls, got %q", n.Status)
			}
			if n.MissedPolls != 3 {
				t.Errorf("expected exactly 3 missed polls, got %d", n.MissedPolls)
			}
			return
		}
	}
	t.Error("unreachable node not found in gossip state")
}

func TestGossipNodeComesBackOnline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Start a mock server that responds to health checks.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"peer","roles":["store"]}`))
	}))
	defer mockServer.Close()

	// Parse mock server address.
	// mockServer.URL is like "http://127.0.0.1:PORT"
	addr := mockServer.Listener.Addr().String()
	host := "127.0.0.1"
	var port int
	fmt.Sscanf(addr, "%s", &addr)
	// Extract port from addr.
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			fmt.Sscanf(addr[i+1:], "%d", &port)
			break
		}
	}

	selfNode := MeshNode{
		Name:    "controller",
		Address: "10.0.0.1",
		Port:    8844,
		Roles:   []string{"controller"},
		Status:  StatusOnline,
	}
	g := NewGossip(selfNode, "", "", time.Now())

	// Add peer as offline.
	g.mu.Lock()
	g.nodes = append(g.nodes, MeshNode{
		Name:        "peer",
		Address:     host,
		Port:        port,
		Roles:       []string{"store"},
		Status:      StatusOffline,
		MissedPolls: 3,
	})
	g.mu.Unlock()

	// Poll — the mock server is up so peer should come back online.
	g.pollPeers()

	nodes := g.Nodes()
	for _, n := range nodes {
		if n.Name == "peer" {
			if n.Status != StatusOnline {
				t.Errorf("expected peer to come back online, got %q", n.Status)
			}
			if n.MissedPolls != 0 {
				t.Errorf("expected 0 missed polls after recovery, got %d", n.MissedPolls)
			}
			return
		}
	}
	t.Error("peer node not found")
}

func TestJoinEndpoint_PushesGossipState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "controller",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	joinReq := JoinRequest{
		Name:    "new-store",
		Address: "10.0.0.5",
		Port:    8844,
		Roles:   []string{"store"},
	}
	body, _ := json.Marshal(joinReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/join", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.handleJoin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the node appears in gossip state.
	nodes := d.gossip.Nodes()
	found := false
	for _, n := range nodes {
		if n.Name == "new-store" && n.Status == StatusOnline {
			found = true
			break
		}
	}
	if !found {
		t.Error("new-store not found in gossip state after join")
	}
}

func TestAddNode_EvictsOldNodeWithSameAddress(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	selfNode := MeshNode{
		Name:    "controller",
		Address: "10.0.0.1",
		Port:    8844,
		Roles:   []string{"controller"},
		Status:  StatusOnline,
	}
	g := NewGossip(selfNode, "", "", time.Now())

	// Start a local server to absorb async gossip pushes (avoids 5s timeout on 10.x.x.x).
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()
	sinkHost, sinkPort := parseHostPort(t, sink)

	// Add a node named "ocilab1" at the sink address.
	g.AddNode(MeshNode{
		Name:    "ocilab1",
		Address: sinkHost,
		Port:    sinkPort,
		Roles:   []string{"controller", "store"},
		Status:  StatusOnline,
	})

	// Same machine re-registers with a new name "localtest" at same address.
	g.AddNode(MeshNode{
		Name:    "localtest",
		Address: sinkHost,
		Port:    sinkPort,
		Roles:   []string{"controller", "store"},
		Status:  StatusOnline,
	})

	nodes := g.Nodes()
	for _, n := range nodes {
		if n.Name == "ocilab1" {
			t.Error("old node name 'ocilab1' should have been evicted after rename")
		}
	}

	// Should have exactly 2 nodes: controller + localtest.
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes (controller + localtest), got %d", len(nodes))
		for _, n := range nodes {
			t.Logf("  node: %s @ %s:%d", n.Name, n.Address, n.Port)
		}
	}
}

// parseHostPort extracts host and port from an httptest.Server.
func parseHostPort(t *testing.T, s *httptest.Server) (string, int) {
	t.Helper()
	addr := s.Listener.Addr().String()
	host := "127.0.0.1"
	var port int
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			fmt.Sscanf(addr[i+1:], "%d", &port)
			break
		}
	}
	return host, port
}

func TestApplyEvent_EvictsOldNodeWithSameAddress(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	selfNode := MeshNode{
		Name:    "controller",
		Address: "10.0.0.1",
		Port:    8844,
		Roles:   []string{"controller"},
		Status:  StatusOnline,
	}
	g := NewGossip(selfNode, "", "", time.Now())

	// Existing node "ocilab1" at 10.0.0.2:8844.
	g.ApplyEvent(Event{
		Type: EventJoin,
		Node: MeshNode{Name: "ocilab1", Address: "10.0.0.2", Port: 8844, Roles: []string{"store"}, Status: StatusOnline},
	})

	// Same address re-announces as "localtest".
	g.ApplyEvent(Event{
		Type: EventJoin,
		Node: MeshNode{Name: "localtest", Address: "10.0.0.2", Port: 8844, Roles: []string{"store"}, Status: StatusOnline},
	})

	nodes := g.Nodes()
	for _, n := range nodes {
		if n.Name == "ocilab1" {
			t.Error("old node 'ocilab1' should have been evicted by same-address re-registration")
		}
	}

	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestNewGossip_DeduplicatesPersistedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Pre-persist state with a duplicate address (simulates rename ghost in mesh.json).
	ghostState := []MeshNode{
		{Name: "oldname", Address: "10.0.0.2", Port: 8844, Roles: []string{"store"}, Status: StatusOnline},
		{Name: "newname", Address: "10.0.0.2", Port: 8844, Roles: []string{"store"}, Status: StatusOnline},
		{Name: "other", Address: "10.0.0.3", Port: 8844, Roles: []string{"store"}, Status: StatusOnline},
	}
	if err := SaveMeshState(ghostState); err != nil {
		t.Fatalf("failed to persist state: %v", err)
	}

	selfNode := MeshNode{
		Name:    "controller",
		Address: "10.0.0.1",
		Port:    8844,
		Roles:   []string{"controller"},
		Status:  StatusOnline,
	}
	g := NewGossip(selfNode, "", "", time.Now())
	nodes := g.Nodes()

	// Should have: controller, newname (last entry for 10.0.0.2), other = 3 nodes.
	// "oldname" should be deduped away.
	for _, n := range nodes {
		if n.Name == "oldname" {
			t.Error("ghost node 'oldname' should have been deduplicated at load time")
		}
	}
	if len(nodes) != 3 {
		t.Errorf("expected 3 nodes (controller + newname + other), got %d", len(nodes))
		for _, n := range nodes {
			t.Logf("  node: %s @ %s:%d", n.Name, n.Address, n.Port)
		}
	}
}

func TestSetNodes_DeduplicatesByAddress(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	selfNode := MeshNode{
		Name:    "controller",
		Address: "10.0.0.1",
		Port:    8844,
		Roles:   []string{"controller"},
		Status:  StatusOnline,
	}
	g := NewGossip(selfNode, "", "", time.Now())

	// SetNodes with duplicate address entries.
	g.SetNodes([]MeshNode{
		selfNode,
		{Name: "stale", Address: "10.0.0.5", Port: 8844, Roles: []string{"store"}, Status: StatusOnline},
		{Name: "fresh", Address: "10.0.0.5", Port: 8844, Roles: []string{"store"}, Status: StatusOnline},
	})

	nodes := g.Nodes()
	for _, n := range nodes {
		if n.Name == "stale" {
			t.Error("stale node should have been deduplicated in SetNodes")
		}
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes (controller + fresh), got %d", len(nodes))
	}
}

func TestPollPeers_UpdatesDiskFreeGB(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Start a mock peer that returns disk_free_gb in health response.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"peer","roles":["store"],"port":8844,"disk_total_gb":500.0,"disk_free_gb":312.5,"uptime_seconds":100}`))
	}))
	defer mockServer.Close()

	// Parse mock server address.
	addr := mockServer.Listener.Addr().String()
	host := "127.0.0.1"
	var port int
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			fmt.Sscanf(addr[i+1:], "%d", &port)
			break
		}
	}

	selfNode := MeshNode{
		Name:    "controller",
		Address: "10.0.0.1",
		Port:    8844,
		Roles:   []string{"controller"},
		Status:  StatusOnline,
	}
	g := NewGossip(selfNode, "", "", time.Now())

	// Add peer as online with no disk info.
	g.ApplyEvent(Event{
		Type: EventJoin,
		Node: MeshNode{Name: "peer", Address: host, Port: port, Roles: []string{"store"}, Status: StatusOnline},
	})

	// Poll — the mock server returns disk_free_gb=312.5.
	g.pollPeers()

	nodes := g.Nodes()
	for _, n := range nodes {
		if n.Name == "peer" {
			if n.DiskFreeGB != 312.5 {
				t.Errorf("expected DiskFreeGB=312.5, got %f", n.DiskFreeGB)
			}
			return
		}
	}
	t.Error("peer node not found")
}
