package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestCmdInventory_JSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	nodes := []daemon.MeshNode{
		{Name: "node1", Address: "REPLACE_NODE1", Port: 0, Roles: []string{"store"}, Status: daemon.StatusOnline, LastSeen: &now},
		{Name: "node2", Address: "REPLACE_NODE2", Port: 0, Roles: []string{"store"}, Status: daemon.StatusOnline, LastSeen: &now},
	}

	inv1 := []daemon.InventoryEntry{
		{RepoID: "meta-llama/Llama-3-8B", Format: "gguf", Quant: "Q4_K_M", SizeBytes: 4_000_000_000, LastAccessed: now},
	}
	inv2 := []daemon.InventoryEntry{
		{RepoID: "mlx-community/Phi-3", Format: "mlx", SizeBytes: 7_500_000_000, LastAccessed: now},
	}

	// Start fake node servers.
	node1Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(inv1)
		}
	}))
	defer node1Server.Close()

	node2Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(inv2)
		}
	}))
	defer node2Server.Close()

	// Parse node server addresses.
	node1Addr, node1Port := parseTestServerAddr(t, node1Server.URL)
	node2Addr, node2Port := parseTestServerAddr(t, node2Server.URL)
	nodes[0].Address = node1Addr
	nodes[0].Port = node1Port
	nodes[1].Address = node2Addr
	nodes[1].Port = node2Port

	// Start fake daemon (returns nodes list).
	daemonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonServer.Close()

	daemonPort := strings.TrimPrefix(daemonServer.URL, "http://127.0.0.1:")

	cfg := &meshconfig.Config{
		Name:      "node1",
		Port:      mustAtoi(t, daemonPort),
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdInventory([]string{"--json"})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	var got []InventoryRow
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, output)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d: %s", len(got), output)
	}
	if got[0].Node != "node1" || got[0].RepoID != "meta-llama/Llama-3-8B" || got[0].Quant != "Q4_K_M" {
		t.Errorf("unexpected row 0: %+v", got[0])
	}
	if got[1].Node != "node2" || got[1].RepoID != "mlx-community/Phi-3" || got[1].Quant != "" {
		t.Errorf("unexpected row 1: %+v", got[1])
	}
	if got[0].Stale || got[1].Stale {
		t.Errorf("expected stale=false for online nodes, got: %+v, %+v", got[0], got[1])
	}
}

func TestCmdInventory_Table(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	nodes := []daemon.MeshNode{
		{Name: "mynode", Address: "REPLACE", Port: 0, Roles: []string{"store"}, Status: daemon.StatusOnline, LastSeen: &now},
	}

	inv := []daemon.InventoryEntry{
		{RepoID: "org/model-a", Format: "safetensors", SizeBytes: 2_000_000_000, LastAccessed: now},
	}

	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(inv)
		}
	}))
	defer nodeServer.Close()

	nodeAddr, nodePort := parseTestServerAddr(t, nodeServer.URL)
	nodes[0].Address = nodeAddr
	nodes[0].Port = nodePort

	daemonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonServer.Close()

	daemonPort := strings.TrimPrefix(daemonServer.URL, "http://127.0.0.1:")
	cfg := &meshconfig.Config{
		Name:      "mynode",
		Port:      mustAtoi(t, daemonPort),
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdInventory([]string{})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "NODE") {
		t.Errorf("missing NODE header in output:\n%s", output)
	}
	if !strings.Contains(output, "MODEL") {
		t.Errorf("missing MODEL header in output:\n%s", output)
	}
	if !strings.Contains(output, "FORMAT") {
		t.Errorf("missing FORMAT header in output:\n%s", output)
	}
	if !strings.Contains(output, "SIZE") {
		t.Errorf("missing SIZE header in output:\n%s", output)
	}
	if !strings.Contains(output, "mynode") {
		t.Errorf("missing node name in output:\n%s", output)
	}
	if !strings.Contains(output, "org/model-a") {
		t.Errorf("missing model name in output:\n%s", output)
	}
	if !strings.Contains(output, "safetensors") {
		t.Errorf("missing format in output:\n%s", output)
	}
}

func TestCmdInventory_OfflineNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	// Node2 is offline — its cached inventory should appear with stale:true.
	nodes := []daemon.MeshNode{
		{Name: "online-node", Address: "REPLACE", Port: 0, Roles: []string{"store"}, Status: daemon.StatusOnline, LastSeen: &now},
		{Name: "offline-node", Address: "10.99.99.99", Port: 9999, Roles: []string{"store"}, Status: daemon.StatusOffline},
	}

	onlineInv := []daemon.InventoryEntry{
		{RepoID: "org/model-x", Format: "gguf", Quant: "Q8_0", SizeBytes: 8_000_000_000, LastAccessed: now},
	}

	// Cached inventory that the local daemon returns for the offline node.
	cachedInv := []daemon.InventoryEntry{
		{RepoID: "MaziyarPanahi/Qwen3-0.6B-GGUF", Format: "gguf", Quant: "Q4_K_M", SizeBytes: 402_000_000, LastAccessed: now},
	}

	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(onlineInv)
		}
	}))
	defer nodeServer.Close()

	nodeAddr, nodePort := parseTestServerAddr(t, nodeServer.URL)
	nodes[0].Address = nodeAddr
	nodes[0].Port = nodePort

	// Local daemon serves both /v1/nodes and /v1/peer-inventory (for stale cache).
	daemonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/nodes":
			json.NewEncoder(w).Encode(nodes)
		case "/v1/peer-inventory":
			if r.URL.Query().Get("node") == "offline-node" {
				json.NewEncoder(w).Encode(cachedInv)
			} else {
				json.NewEncoder(w).Encode([]daemon.InventoryEntry{})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemonServer.Close()

	daemonPort := strings.TrimPrefix(daemonServer.URL, "http://127.0.0.1:")
	cfg := &meshconfig.Config{
		Name:      "online-node",
		Port:      mustAtoi(t, daemonPort),
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	// Capture both stdout and stderr.
	oldOut := os.Stdout
	rOut, wOut, _ := os.Pipe()
	os.Stdout = wOut

	oldErr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	code := cmdInventory([]string{"--json"})

	wOut.Close()
	os.Stdout = oldOut
	wErr.Close()
	os.Stderr = oldErr

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var bufOut bytes.Buffer
	bufOut.ReadFrom(rOut)
	output := bufOut.String()

	var bufErr bytes.Buffer
	bufErr.ReadFrom(rErr)
	stderrOutput := bufErr.String()

	var got []InventoryRow
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, output)
	}

	// Both online entry AND cached offline entry should appear.
	if len(got) != 2 {
		t.Fatalf("expected 2 rows (1 online + 1 stale), got %d: %s", len(got), output)
	}

	// Rows are sorted by node name: "offline-node" < "online-node".
	staleRow := got[0]
	onlineRow := got[1]

	// Online row: stale=false.
	if onlineRow.Node != "online-node" {
		t.Errorf("expected row 1 node='online-node', got %q", onlineRow.Node)
	}
	if onlineRow.Stale {
		t.Errorf("expected stale=false for online node")
	}

	// Offline (stale) row.
	if staleRow.Node != "offline-node" {
		t.Errorf("expected row 0 node='offline-node', got %q", staleRow.Node)
	}
	if !staleRow.Stale {
		t.Errorf("expected stale=true for offline node, got false")
	}
	if staleRow.RepoID != "MaziyarPanahi/Qwen3-0.6B-GGUF" {
		t.Errorf("expected cached repo_id, got %q", staleRow.RepoID)
	}

	// Verify warning was emitted on stderr (not stdout).
	if !strings.Contains(stderrOutput, "warning: offline-node unreachable") {
		t.Errorf("expected warning about offline-node on stderr, got: %q", stderrOutput)
	}
}

// TestCmdInventory_OfflineNode_NoCache verifies that when an offline node has no
// cached inventory (cache empty), the output contains only online entries and the
// warning still goes to stderr (#211, #212).
func TestCmdInventory_OfflineNode_NoCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	nodes := []daemon.MeshNode{
		{Name: "online-node", Address: "REPLACE", Port: 0, Roles: []string{"store"}, Status: daemon.StatusOnline, LastSeen: &now},
		{Name: "offline-node", Address: "10.99.99.99", Port: 9999, Roles: []string{"store"}, Status: daemon.StatusOffline},
	}

	onlineInv := []daemon.InventoryEntry{
		{RepoID: "org/model-z", Format: "gguf", Quant: "Q4_K_M", SizeBytes: 4_000_000_000, LastAccessed: now},
	}

	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(onlineInv)
		}
	}))
	defer nodeServer.Close()

	nodeAddr, nodePort := parseTestServerAddr(t, nodeServer.URL)
	nodes[0].Address = nodeAddr
	nodes[0].Port = nodePort

	// Local daemon returns empty cache for the offline node.
	daemonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/nodes":
			json.NewEncoder(w).Encode(nodes)
		case "/v1/peer-inventory":
			json.NewEncoder(w).Encode([]daemon.InventoryEntry{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemonServer.Close()

	daemonPort := strings.TrimPrefix(daemonServer.URL, "http://127.0.0.1:")
	cfg := &meshconfig.Config{
		Name:      "online-node",
		Port:      mustAtoi(t, daemonPort),
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	oldOut := os.Stdout
	rOut, wOut, _ := os.Pipe()
	os.Stdout = wOut

	oldErr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	code := cmdInventory([]string{"--json"})

	wOut.Close()
	os.Stdout = oldOut
	wErr.Close()
	os.Stderr = oldErr

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var bufOut bytes.Buffer
	bufOut.ReadFrom(rOut)
	output := bufOut.String()

	var bufErr bytes.Buffer
	bufErr.ReadFrom(rErr)
	stderrOutput := bufErr.String()

	var got []InventoryRow
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, output)
	}

	// Only the online node's model appears when cache is empty.
	if len(got) != 1 {
		t.Fatalf("expected 1 row (online only, no cache), got %d: %s", len(got), output)
	}
	if got[0].Node != "online-node" {
		t.Errorf("expected 'online-node', got %q", got[0].Node)
	}

	// Warning must go to stderr, not stdout (stdout must be valid JSON).
	if !strings.Contains(stderrOutput, "warning: offline-node unreachable") {
		t.Errorf("expected warning on stderr, got: %q", stderrOutput)
	}
}

func TestCmdInventory_OfflineStatusButReachable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	// Node has StatusOffline in gossip (first poll pending) but is actually reachable.
	nodes := []daemon.MeshNode{
		{Name: "local-node", Address: "REPLACE_LOCAL", Port: 0, Roles: []string{"store"}, Status: daemon.StatusOnline, LastSeen: &now},
		{Name: "new-peer", Address: "REPLACE_PEER", Port: 0, Roles: []string{"store"}, Status: daemon.StatusOffline},
	}

	localInv := []daemon.InventoryEntry{
		{RepoID: "org/model-a", Format: "gguf", Quant: "Q4_K_M", SizeBytes: 4_000_000_000, LastAccessed: now},
	}
	peerInv := []daemon.InventoryEntry{
		{RepoID: "org/model-b", Format: "mlx", SizeBytes: 7_000_000_000, LastAccessed: now},
	}

	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(localInv)
		}
	}))
	defer localServer.Close()

	// Peer server is running (reachable), even though gossip says offline.
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(peerInv)
		}
	}))
	defer peerServer.Close()

	localAddr, localPort := parseTestServerAddr(t, localServer.URL)
	peerAddr, peerPort := parseTestServerAddr(t, peerServer.URL)
	nodes[0].Address = localAddr
	nodes[0].Port = localPort
	nodes[1].Address = peerAddr
	nodes[1].Port = peerPort

	daemonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonServer.Close()

	daemonPort := strings.TrimPrefix(daemonServer.URL, "http://127.0.0.1:")
	cfg := &meshconfig.Config{
		Name:      "local-node",
		Port:      mustAtoi(t, daemonPort),
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	oldOut := os.Stdout
	rOut, wOut, _ := os.Pipe()
	os.Stdout = wOut

	oldErr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	code := cmdInventory([]string{"--json"})

	wOut.Close()
	os.Stdout = oldOut
	wErr.Close()
	os.Stderr = oldErr

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var bufOut bytes.Buffer
	bufOut.ReadFrom(rOut)
	output := bufOut.String()

	var bufErr bytes.Buffer
	bufErr.ReadFrom(rErr)
	stderrOutput := bufErr.String()

	var got []InventoryRow
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, output)
	}
	// Both nodes' models should appear — peer is reachable despite offline gossip status.
	if len(got) != 2 {
		t.Fatalf("expected 2 rows (peer reachable despite offline status), got %d: %s", len(got), output)
	}
	// No warning should appear since the request succeeded.
	if strings.Contains(stderrOutput, "warning") {
		t.Errorf("expected no warning for reachable peer, got: %q", stderrOutput)
	}
}

func TestCmdInventory_Empty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	nodes := []daemon.MeshNode{
		{Name: "empty", Address: "REPLACE", Port: 0, Roles: []string{"store"}, Status: daemon.StatusOnline},
	}

	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]daemon.InventoryEntry{})
		}
	}))
	defer nodeServer.Close()

	nodeAddr, nodePort := parseTestServerAddr(t, nodeServer.URL)
	nodes[0].Address = nodeAddr
	nodes[0].Port = nodePort

	daemonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonServer.Close()

	daemonPort := strings.TrimPrefix(daemonServer.URL, "http://127.0.0.1:")
	cfg := &meshconfig.Config{
		Name:      "empty",
		Port:      mustAtoi(t, daemonPort),
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdInventory([]string{})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "No models reported by online nodes") {
		t.Errorf("expected 'No models reported by online nodes' message, got:\n%s", output)
	}
}

// TestInventoryJSONOfflineNodeWarningOnStderr verifies that when --json is active
// and a node is unreachable, the warning goes to stderr (not stdout) so that
// the JSON array on stdout remains parseable (#233).
func TestInventoryJSONOfflineNodeWarningOnStderr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now()
	nodes := []daemon.MeshNode{
		{Name: "mini1", Address: "10.99.99.99", Port: 9999, Roles: []string{"store"}, Status: daemon.StatusOffline},
	}

	daemonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/nodes":
			json.NewEncoder(w).Encode(nodes)
		case "/v1/peer-inventory":
			json.NewEncoder(w).Encode([]daemon.InventoryEntry{
				{RepoID: "org/model", Format: "gguf", Quant: "Q4_K_M", SizeBytes: 100, LastAccessed: now},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemonServer.Close()

	daemonPort := strings.TrimPrefix(daemonServer.URL, "http://127.0.0.1:")
	cfg := &meshconfig.Config{
		Name:      "self",
		Port:      mustAtoi(t, daemonPort),
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	oldOut := os.Stdout
	rOut, wOut, _ := os.Pipe()
	os.Stdout = wOut

	oldErr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	code := cmdInventory([]string{"--json"})

	wOut.Close()
	os.Stdout = oldOut
	wErr.Close()
	os.Stderr = oldErr

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var bufOut bytes.Buffer
	bufOut.ReadFrom(rOut)
	stdout := bufOut.String()

	var bufErr bytes.Buffer
	bufErr.ReadFrom(rErr)
	stderr := bufErr.String()

	// stdout must be valid JSON.
	var rows []InventoryRow
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("stdout must be valid JSON, got: %q\nerr: %v", stdout, err)
	}

	// Warning must be on stderr, not stdout.
	if !strings.Contains(stderr, "warning:") {
		t.Errorf("expected warning on stderr for unreachable node, got: %q", stderr)
	}
	if strings.Contains(stdout, "warning:") {
		t.Errorf("warning must not appear on stdout when --json is active, got: %q", stdout)
	}
}

func TestCmdInventory_NoConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdInventory([]string{})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestCmdInventory_JSON_EmptyReturnsArray(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	nodes := []daemon.MeshNode{
		{Name: "empty", Address: "REPLACE", Port: 0, Roles: []string{"store"}, Status: daemon.StatusOnline},
	}

	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/inventory" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]daemon.InventoryEntry{})
		}
	}))
	defer nodeServer.Close()

	nodeAddr, nodePort := parseTestServerAddr(t, nodeServer.URL)
	nodes[0].Address = nodeAddr
	nodes[0].Port = nodePort

	daemonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonServer.Close()

	daemonPort := strings.TrimPrefix(daemonServer.URL, "http://127.0.0.1:")
	cfg := &meshconfig.Config{
		Name:      "empty",
		Port:      mustAtoi(t, daemonPort),
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdInventory([]string{"--json"})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Must be [] not null.
	if output != "[]\n" {
		t.Errorf("expected []\\n for empty inventory, got %q", output)
	}
}

func TestCmdInventory_DaemonNotRunning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test",
		Port:      19999,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := cmdInventory([]string{})

	w.Close()
	os.Stderr = old

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "daemon not running") {
		t.Errorf("expected 'daemon not running' error, got: %s", output)
	}
	if strings.Contains(output, "127.0.0.1") || strings.Contains(output, "http://") {
		t.Errorf("error must not expose raw URL or IP address, got: %s", output)
	}
}

func TestCmdInventory_JSON_DaemonNotRunning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test",
		Port:      19997,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdInventory([]string{"--json"})

	w.Close()
	os.Stdout = old

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	var errResp map[string]string
	if err := json.Unmarshal([]byte(output), &errResp); err != nil {
		t.Fatalf("expected JSON error output, got: %s", output)
	}
	if errResp["error"] == "" {
		t.Errorf("expected non-empty 'error' field, got: %v", errResp)
	}
}

// TestInventoryTableNoTruncationWithStaleNode verifies that when a node is stale
// (its name has " (stale)" appended, widening the NODE column), the MODEL column
// is not shrunk to the point where the quant suffix is cut off (#238).
func TestInventoryTableNoTruncationWithStaleNode(t *testing.T) {
	// Construct a row whose full model+quant string is long enough that the old
	// column-shrink algorithm would sacrifice the quant suffix, but the fix
	// ensures MODEL is never shrunk (other columns absorb the overflow instead).
	rows := []InventoryRow{
		{
			Node:      "node1",
			RepoID:    "BigOrg/VeryLongModelNameThatExceedsLimit-GGUF",
			Quant:     "Q4_K_M",
			Format:    "gguf",
			SizeBytes: 746_000_000,
			Stale:     true,
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printInventoryTable(rows)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Full quant suffix must appear intact — not cut to "Q4…" or "Q4_K…".
	if !strings.Contains(output, "Q4_K_M") {
		t.Errorf("quant suffix Q4_K_M should not be truncated; got:\n%s", output)
	}
}

func TestSortInventoryRows(t *testing.T) {
	rows := []InventoryRow{
		{Node: "node2", Format: "mlx", RepoID: "b/model"},
		{Node: "node1", Format: "safetensors", RepoID: "a/model"},
		{Node: "node1", Format: "gguf", RepoID: "z/model"},
		{Node: "node1", Format: "mlx", RepoID: "b/model"},
		{Node: "node1", Format: "gguf", RepoID: "a/model"},
		{Node: "node2", Format: "gguf", RepoID: "a/model"},
	}
	sortInventoryRows(rows)

	want := []InventoryRow{
		{Node: "node1", Format: "gguf", RepoID: "a/model"},
		{Node: "node1", Format: "gguf", RepoID: "z/model"},
		{Node: "node1", Format: "mlx", RepoID: "b/model"},
		{Node: "node1", Format: "safetensors", RepoID: "a/model"},
		{Node: "node2", Format: "gguf", RepoID: "a/model"},
		{Node: "node2", Format: "mlx", RepoID: "b/model"},
	}
	for i, r := range rows {
		if r.Node != want[i].Node || r.Format != want[i].Format || r.RepoID != want[i].RepoID {
			t.Errorf("row %d: got {%s %s %s}, want {%s %s %s}",
				i, r.Node, r.Format, r.RepoID, want[i].Node, want[i].Format, want[i].RepoID)
		}
	}
}

// parseTestServerAddr extracts host and port from a test server URL like "http://127.0.0.1:12345".
func parseTestServerAddr(t *testing.T, url string) (string, int) {
	t.Helper()
	// URL format: http://127.0.0.1:PORT
	addr := strings.TrimPrefix(url, "http://")
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		t.Fatalf("unexpected test server URL format: %s", url)
	}
	var port int
	if _, err := fmt.Sscanf(parts[1], "%d", &port); err != nil {
		t.Fatalf("failed to parse port from %s: %v", url, err)
	}
	return parts[0], port
}

// TestInventoryTableFormatColumnFitsSafetensors verifies that the FORMAT column
// is wide enough to display "safetensors" (11 chars) without truncation (#258).
func TestInventoryTableFormatColumnFitsSafetensors(t *testing.T) {
	rows := []InventoryRow{
		{Node: "ocilab1", RepoID: "Qwen/Qwen3-0.6B", Format: "safetensors", SizeBytes: 1400000000},
	}
	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printInventoryTable(rows)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// "safetensors" must appear in full, not truncated.
	if !strings.Contains(output, "safetensors") {
		t.Errorf("FORMAT column truncated 'safetensors'; output:\n%s", output)
	}
}
