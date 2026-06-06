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
	// Node2 is offline — should show as stale with no entries.
	nodes := []daemon.MeshNode{
		{Name: "online-node", Address: "REPLACE", Port: 0, Roles: []string{"store"}, Status: daemon.StatusOnline, LastSeen: &now},
		{Name: "offline-node", Address: "10.99.99.99", Port: 9999, Roles: []string{"store"}, Status: daemon.StatusOffline},
	}

	inv := []daemon.InventoryEntry{
		{RepoID: "org/model-x", Format: "gguf", Quant: "Q8_0", SizeBytes: 8_000_000_000, LastAccessed: now},
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
	// Only the online node's models should appear.
	if len(got) != 1 {
		t.Fatalf("expected 1 row (from online node), got %d: %s", len(got), output)
	}
	if got[0].Node != "online-node" {
		t.Errorf("expected node 'online-node', got %q", got[0].Node)
	}
	if got[0].Stale {
		t.Errorf("expected stale=false for online node")
	}

	// Verify warning was emitted on stderr.
	if !strings.Contains(stderrOutput, "warning: offline-node unreachable") {
		t.Errorf("expected warning about offline-node on stderr, got: %q", stderrOutput)
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
