package main

import (
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

func TestCmdLeave_Success(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Set up local mesh config.
	cfg := &meshconfig.Config{
		Name:      "leaving-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
		Seeds:     []string{"peer1:8844"},
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)
	meshconfig.WriteMeshKey("test-key-123")

	// Set up a fake peer that records leave events.
	var receivedEvent daemon.Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events" && r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(&receivedEvent)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok": true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Persist mesh state with peer pointing to test server.
	peerAddr := strings.TrimPrefix(server.URL, "http://")
	parts := strings.Split(peerAddr, ":")
	peerHost := parts[0]
	var peerPort int
	fmt.Sscanf(parts[1], "%d", &peerPort)

	nodes := []daemon.MeshNode{
		{Name: "leaving-node", Address: "localhost", Port: 8844, Roles: []string{"store"}, Status: daemon.StatusOnline},
		{Name: "peer1", Address: peerHost, Port: peerPort, Roles: []string{"controller"}, Status: daemon.StatusOnline},
	}
	daemon.SaveMeshState(nodes)

	code := cmdLeave([]string{})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify leave event was sent.
	if receivedEvent.Type != daemon.EventLeave {
		t.Errorf("expected EventLeave, got %q", receivedEvent.Type)
	}
	if receivedEvent.Node.Name != "leaving-node" {
		t.Errorf("expected node name 'leaving-node', got %q", receivedEvent.Node.Name)
	}

	// Verify mesh.json was removed.
	statePath := meshconfig.MeshStatePath()
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("mesh.json should be removed, but still exists")
	}

	// Verify mesh.key was removed.
	keyPath := meshconfig.MeshKeyPath()
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("mesh.key should be removed, but still exists")
	}

	// Verify config.toml still exists but seeds are cleared.
	updatedCfg, err := meshconfig.LoadFrom(meshconfig.ConfigPath())
	if err != nil {
		t.Fatalf("config.toml should still exist: %v", err)
	}
	if len(updatedCfg.Seeds) != 0 {
		t.Errorf("seeds should be cleared, got %v", updatedCfg.Seeds)
	}
	// Name, roles, shelf_root should be preserved.
	if updatedCfg.Name != "leaving-node" {
		t.Errorf("name should be preserved, got %q", updatedCfg.Name)
	}
	if len(updatedCfg.Roles) != 1 || updatedCfg.Roles[0] != "store" {
		t.Errorf("roles should be preserved, got %v", updatedCfg.Roles)
	}
}

func TestCmdLeave_NoMeshConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdLeave([]string{})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestCmdLeave_Help(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdLeave([]string{"--help"})
	if code != 0 {
		t.Fatalf("expected exit 0 for help, got %d", code)
	}
}
