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

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestCmdNodes_JSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Start a fake daemon.
	nodes := []daemon.MeshNode{
		{Name: "node1", Address: "10.0.0.1", Port: 8844, Roles: []string{"controller", "store"}, Status: daemon.StatusOnline, DiskFreeGB: 752.95},
		{Name: "node2", Address: "10.0.0.2", Port: 8844, Roles: []string{"executor"}, Status: daemon.StatusOffline},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer server.Close()

	// Extract port from test server.
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")

	// Write config with matching port.
	cfg := &meshconfig.Config{
		Name:      "node1",
		Port:      mustAtoi(t, port),
		Roles:     []string{"controller", "store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdNodes([]string{"--json"})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	var got []daemon.MeshNode
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, output)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(got))
	}
	if got[0].Name != "node1" || got[1].Name != "node2" {
		t.Errorf("unexpected node names: %v", got)
	}
	if got[0].DiskFreeGB != 752.95 {
		t.Errorf("expected node1 disk_free_gb=752.95, got %f", got[0].DiskFreeGB)
	}
	if got[1].Status != daemon.StatusOffline {
		t.Errorf("expected node2 offline, got %s", got[1].Status)
	}
}

func TestCmdNodes_Table(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	nodes := []daemon.MeshNode{
		{Name: "ctrl", Address: "10.0.0.1", Port: 8844, Roles: []string{"controller"}, Status: daemon.StatusOnline, DiskFreeGB: 500.5},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer server.Close()

	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	cfg := &meshconfig.Config{
		Name:      "ctrl",
		Port:      mustAtoi(t, port),
		Roles:     []string{"controller"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdNodes([]string{})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Check header and data are present.
	if !strings.Contains(output, "NAME") {
		t.Errorf("missing NAME header in output:\n%s", output)
	}
	if !strings.Contains(output, "ROLES") {
		t.Errorf("missing ROLES header in output:\n%s", output)
	}
	if !strings.Contains(output, "STATUS") {
		t.Errorf("missing STATUS header in output:\n%s", output)
	}
	if !strings.Contains(output, "ctrl") {
		t.Errorf("missing node name in output:\n%s", output)
	}
	if !strings.Contains(output, "online") {
		t.Errorf("missing status in output:\n%s", output)
	}
	if !strings.Contains(output, "500.5 GB") {
		t.Errorf("missing disk free value in output:\n%s", output)
	}
}

func TestCmdNodes_DaemonNotRunning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Write config pointing to a port nothing listens on.
	cfg := &meshconfig.Config{
		Name:      "test",
		Port:      19999,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	// Capture stderr.
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := cmdNodes([]string{})

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

func TestCmdNodes_NoConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdNodes([]string{})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatalf("mustAtoi(%q): %v", s, err)
	}
	return n
}
