package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestCmdRole_Set(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	code := cmdRole([]string{"set", "controller,executor"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	updatedCfg, err := meshconfig.LoadFrom(meshconfig.ConfigPath())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if len(updatedCfg.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %v", updatedCfg.Roles)
	}
	roleSet := map[string]bool{}
	for _, r := range updatedCfg.Roles {
		roleSet[r] = true
	}
	if !roleSet["controller"] || !roleSet["executor"] {
		t.Errorf("expected controller and executor, got %v", updatedCfg.Roles)
	}
}

func TestCmdRole_Add(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	code := cmdRole([]string{"add", "executor"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	updatedCfg, err := meshconfig.LoadFrom(meshconfig.ConfigPath())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if len(updatedCfg.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %v", updatedCfg.Roles)
	}
	roleSet := map[string]bool{}
	for _, r := range updatedCfg.Roles {
		roleSet[r] = true
	}
	if !roleSet["store"] || !roleSet["executor"] {
		t.Errorf("expected store and executor, got %v", updatedCfg.Roles)
	}
}

func TestCmdRole_Remove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store", "executor"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	code := cmdRole([]string{"remove", "executor"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	updatedCfg, err := meshconfig.LoadFrom(meshconfig.ConfigPath())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if len(updatedCfg.Roles) != 1 || updatedCfg.Roles[0] != "store" {
		t.Errorf("expected [store], got %v", updatedCfg.Roles)
	}
}

func TestCmdRole_RemoveAll_Fails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	code := cmdRole([]string{"remove", "store"})
	if code != 1 {
		t.Fatalf("expected exit 1 (can't remove all roles), got %d", code)
	}
}

func TestCmdRole_NoMeshConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdRole([]string{"set", "store"})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestCmdRole_InvalidRole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	code := cmdRole([]string{"set", "bogus"})
	if code != 1 {
		t.Fatalf("expected exit 1 for invalid role, got %d", code)
	}
}

func TestCmdRole_GossipsChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)
	meshconfig.WriteMeshKey("key123")

	// Set up a fake peer.
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

	peerAddr := strings.TrimPrefix(server.URL, "http://")
	parts := strings.Split(peerAddr, ":")
	peerHost := parts[0]
	var peerPort int
	fmt.Sscanf(parts[1], "%d", &peerPort)

	nodes := []daemon.MeshNode{
		{Name: "test-node", Address: "localhost", Port: 8844, Roles: []string{"store"}, Status: daemon.StatusOnline},
		{Name: "peer1", Address: peerHost, Port: peerPort, Roles: []string{"controller"}, Status: daemon.StatusOnline},
	}
	daemon.SaveMeshState(nodes)

	// Reload config to get mesh key.
	code := cmdRole([]string{"add", "executor"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify event was gossiped.
	if receivedEvent.Type != daemon.EventHealthChange {
		t.Errorf("expected EventHealthChange, got %q", receivedEvent.Type)
	}
	if receivedEvent.Node.Name != "test-node" {
		t.Errorf("expected node name 'test-node', got %q", receivedEvent.Node.Name)
	}
	// Check that the new roles are in the event.
	roleSet := map[string]bool{}
	for _, r := range receivedEvent.Node.Roles {
		roleSet[r] = true
	}
	if !roleSet["store"] || !roleSet["executor"] {
		t.Errorf("expected roles [store, executor] in event, got %v", receivedEvent.Node.Roles)
	}
}

func TestCmdRole_Help(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdRole([]string{"--help"})
	if code != 0 {
		t.Fatalf("expected exit 0 for help, got %d", code)
	}
}

func TestCmdRole_NoArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code := cmdRole([]string{})
	if code != 0 {
		t.Fatalf("expected exit 0 for no-arg help display, got %d", code)
	}
}

func TestRoleSetJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdRole([]string{"set", "controller,executor", "--json"})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var got struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, buf.String())
	}
	roleSet := map[string]bool{}
	for _, r := range got.Roles {
		roleSet[r] = true
	}
	if !roleSet["controller"] || !roleSet["executor"] {
		t.Errorf("expected roles [controller, executor], got %v", got.Roles)
	}
}

func TestRoleAddJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdRole([]string{"add", "executor", "--json"})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	out, _ := io.ReadAll(r)
	var got struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, out)
	}
	roleSet := map[string]bool{}
	for _, r := range got.Roles {
		roleSet[r] = true
	}
	if !roleSet["store"] || !roleSet["executor"] {
		t.Errorf("expected roles [store, executor], got %v", got.Roles)
	}
}

func TestRoleRemoveJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store", "executor"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdRole([]string{"remove", "executor", "--json"})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	out, _ := io.ReadAll(r)
	var got struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, out)
	}
	if len(got.Roles) != 1 || got.Roles[0] != "store" {
		t.Errorf("expected roles [store], got %v", got.Roles)
	}
}

func TestCmdRole_SubcommandHelp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, action := range []string{"set", "add", "remove", "get"} {
		for _, flag := range []string{"--help", "-h"} {
			code := cmdRole([]string{action, flag})
			if code != 0 {
				t.Errorf("role %s %s: expected exit 0, got %d", action, flag, code)
			}
		}
	}
}

func TestCmdRole_Get(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"controller", "store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdRole([]string{"get"})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "roles [controller, store]") {
		t.Errorf("expected roles [controller, store], got %q", string(out))
	}
}

func TestCmdRole_GetJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"controller", "store", "executor"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdRole([]string{"get", "--json"})

	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var got struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, buf.String())
	}
	if len(got.Roles) != 3 {
		t.Fatalf("expected 3 roles, got %d", len(got.Roles))
	}
	roleSet := map[string]bool{}
	for _, r := range got.Roles {
		roleSet[r] = true
	}
	if !roleSet["controller"] || !roleSet["store"] || !roleSet["executor"] {
		t.Errorf("expected controller, store, executor, got %v", got.Roles)
	}
}

func TestCmdRole_GetDoesNotModify(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"controller"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	meshconfig.WriteTo(meshconfig.ConfigPath(), cfg)

	code := cmdRole([]string{"get"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// Verify config was not modified.
	updatedCfg, err := meshconfig.LoadFrom(meshconfig.ConfigPath())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if len(updatedCfg.Roles) != 1 || updatedCfg.Roles[0] != "controller" {
		t.Errorf("config should be unchanged, got %v", updatedCfg.Roles)
	}
}

func TestCmdRole_GetRequiresNoRolesArg(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	code := cmdRole([]string{"get", "bogus"})
	if code != 1 {
		t.Fatalf("expected exit 1 (get takes no roles arg), got %d", code)
	}
}


