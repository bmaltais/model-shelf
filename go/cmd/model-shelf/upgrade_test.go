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
	"sync"
	"testing"
	"time"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
	"github.com/alexziskind1/model-shelf/internal/selfupgrade"
)

func TestIsControllerNode(t *testing.T) {
	cases := []struct {
		roles []string
		want  bool
	}{
		{[]string{"controller"}, true},
		{[]string{"controller", "store"}, true},
		{[]string{"store", "executor"}, false},
		{[]string{}, false},
	}
	for _, c := range cases {
		cfg := &meshconfig.Config{Roles: c.roles}
		if got := isControllerNode(cfg); got != c.want {
			t.Errorf("isControllerNode(%v) = %v, want %v", c.roles, got, c.want)
		}
	}
}

func TestNodeVersion(t *testing.T) {
	if nodeVersion("") != "unknown" {
		t.Error("empty version should return 'unknown'")
	}
	if nodeVersion("1.2.3") != "1.2.3" {
		t.Error("non-empty version should be returned unchanged")
	}
}

func TestFetchPeerVersion_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"version": "1.2.3"})
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 3 * time.Second}
	ver, ok := fetchPeerVersion(srv.URL, "", client)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ver != "1.2.3" {
		t.Errorf("expected version 1.2.3, got %s", ver)
	}
}

func TestFetchPeerVersion_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 3 * time.Second}
	_, ok := fetchPeerVersion(srv.URL, "", client)
	if ok {
		t.Fatal("expected ok=false for non-200 response")
	}
}

func TestFetchPeerVersion_ConnectionRefused(t *testing.T) {
	client := &http.Client{Timeout: 3 * time.Second}
	_, ok := fetchPeerVersion("http://127.0.0.1:1", "", client)
	if ok {
		t.Fatal("expected ok=false for connection refused")
	}
}

func TestUpgradePeerNode_AlreadyCurrent(t *testing.T) {
	peer := daemon.MeshNode{Name: "node-a", Address: "127.0.0.1"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/upgrade" && r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(map[string]string{"status": "already_current"})
		}
	}))
	defer srv.Close()

	port := parsePort(t, srv.URL)
	peer.Port = port

	r := upgradePeerNode(peer, "1.0.0", "")
	if r.Status != "upgraded" {
		t.Errorf("expected 'upgraded' for already_current, got %q", r.Status)
	}
}

func TestUpgradePeerNode_UpgradeAndPollSuccess(t *testing.T) {
	peer := daemon.MeshNode{Name: "node-b", Address: "127.0.0.1"}

	// Peer reports old version on first health poll, new version on second.
	pollCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/upgrade" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		case r.URL.Path == "/v1/health" && r.Method == http.MethodGet:
			pollCount++
			ver := "0.9.0"
			if pollCount >= 2 {
				ver = "1.0.0"
			}
			json.NewEncoder(w).Encode(map[string]string{"version": ver})
		}
	}))
	defer srv.Close()

	peer.Port = parsePort(t, srv.URL)

	r := upgradePeerNode(peer, "1.0.0", "")
	if r.Status != "upgraded" {
		t.Errorf("expected 'upgraded', got %q (reason: %s)", r.Status, r.Reason)
	}
}

func TestUpgradePeerNode_ConnectionRefused(t *testing.T) {
	peer := daemon.MeshNode{Name: "node-c", Address: "127.0.0.1", Port: 1}
	r := upgradePeerNode(peer, "1.0.0", "")
	if r.Status != "failed" {
		t.Errorf("expected 'failed' for connection refused, got %q", r.Status)
	}
	if r.Name != "node-c" {
		t.Errorf("expected name 'node-c', got %q", r.Name)
	}
}

func TestRunMeshUpgrade_JSONOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Override self-upgrade to avoid real network calls.
	orig := runSelfUpgradeFunc
	t.Cleanup(func() { runSelfUpgradeFunc = orig })
	runSelfUpgradeFunc = func(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
		return nil
	}

	// Fake peer that accepts upgrade and returns new version on health poll.
	pollCount := 0
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/upgrade" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		case r.URL.Path == "/v1/health":
			pollCount++
			ver := "0.5.0"
			if pollCount >= 1 {
				ver = "1.0.0"
			}
			json.NewEncoder(w).Encode(map[string]string{"version": ver})
		}
	}))
	defer peerSrv.Close()

	peerAddr := "127.0.0.1"
	peerPort := parsePort(t, peerSrv.URL)

	// Fake local daemon returning two nodes: self (controller) + one online peer + one offline peer.
	nodes := []daemon.MeshNode{
		{Name: "ctrl", Address: peerAddr, Port: 8844, Roles: []string{"controller"}, Status: daemon.StatusOnline, Version: "0.5.0"},
		{Name: "peer-online", Address: peerAddr, Port: peerPort, Roles: []string{"store"}, Status: daemon.StatusOnline, Version: "0.5.0"},
		{Name: "peer-offline", Address: peerAddr, Port: 9999, Roles: []string{"executor"}, Status: daemon.StatusOffline, Version: "0.4.0"},
	}
	daemonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonSrv.Close()

	cfg := &meshconfig.Config{
		Name:      "ctrl",
		Port:      parsePort(t, daemonSrv.URL),
		Roles:     []string{"controller"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	// Capture stdout.
	oldOut := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := runMeshUpgrade(cfg, "1.0.0", true, false, true)

	w.Close()
	os.Stdout = oldOut

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	var result meshUpgradeOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}

	if result.TargetVersion != "1.0.0" {
		t.Errorf("expected target_version 1.0.0, got %s", result.TargetVersion)
	}
	if len(result.Nodes) != 3 {
		t.Fatalf("expected 3 nodes in result, got %d: %+v", len(result.Nodes), result.Nodes)
	}

	// Find each node result by name.
	byName := make(map[string]nodeUpgradeResult, len(result.Nodes))
	for _, n := range result.Nodes {
		byName[n.Name] = n
	}

	if byName["peer-online"].Status != "upgraded" {
		t.Errorf("peer-online: expected 'upgraded', got %q", byName["peer-online"].Status)
	}
	if byName["peer-offline"].Status != "skipped" {
		t.Errorf("peer-offline: expected 'skipped', got %q", byName["peer-offline"].Status)
	}
	if byName["peer-offline"].Reason != "offline" {
		t.Errorf("peer-offline: expected reason 'offline', got %q", byName["peer-offline"].Reason)
	}
	if byName["ctrl"].Status != "upgraded" {
		t.Errorf("ctrl: expected 'upgraded', got %q", byName["ctrl"].Status)
	}
}

func TestRunMeshUpgrade_ControllerSelfUpgradesLast(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var upgradeOrder []string
	var orderMu = make(chan struct{}, 1)
	orderMu <- struct{}{}

	orig := runSelfUpgradeFunc
	t.Cleanup(func() { runSelfUpgradeFunc = orig })

	// Fake peer: upgrade takes 50ms before health returns new version.
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/upgrade" && r.Method == http.MethodPost:
			<-orderMu
			upgradeOrder = append(upgradeOrder, "peer")
			orderMu <- struct{}{}
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		case r.URL.Path == "/v1/health":
			json.NewEncoder(w).Encode(map[string]string{"version": "2.0.0"})
		}
	}))
	defer peerSrv.Close()

	runSelfUpgradeFunc = func(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
		<-orderMu
		upgradeOrder = append(upgradeOrder, "self")
		orderMu <- struct{}{}
		return nil
	}

	nodes := []daemon.MeshNode{
		{Name: "ctrl", Address: "127.0.0.1", Port: 8844, Roles: []string{"controller"}, Status: daemon.StatusOnline},
		{Name: "peer1", Address: "127.0.0.1", Port: parsePort(t, peerSrv.URL), Roles: []string{"store"}, Status: daemon.StatusOnline},
	}
	daemonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonSrv.Close()

	cfg := &meshconfig.Config{
		Name:      "ctrl",
		Port:      parsePort(t, daemonSrv.URL),
		Roles:     []string{"controller"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	runMeshUpgrade(cfg, "2.0.0", true, false, true)

	if len(upgradeOrder) < 2 {
		t.Fatalf("expected at least 2 upgrade events, got %v", upgradeOrder)
	}
	last := upgradeOrder[len(upgradeOrder)-1]
	if last != "self" {
		t.Errorf("controller self-upgrade should be last, got order: %v", upgradeOrder)
	}
}

func TestRunMeshUpgrade_NoPeers_StillSelfUpgrades(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	orig := runSelfUpgradeFunc
	t.Cleanup(func() { runSelfUpgradeFunc = orig })
	selfCalled := false
	runSelfUpgradeFunc = func(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
		selfCalled = true
		return nil
	}

	// Daemon returns only self (no peers).
	nodes := []daemon.MeshNode{
		{Name: "ctrl", Address: "127.0.0.1", Port: 8844, Roles: []string{"controller"}, Status: daemon.StatusOnline},
	}
	daemonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonSrv.Close()

	cfg := &meshconfig.Config{
		Name:      "ctrl",
		Port:      parsePort(t, daemonSrv.URL),
		Roles:     []string{"controller"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	code := runMeshUpgrade(cfg, "1.0.0", true, false, true)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !selfCalled {
		t.Error("expected self-upgrade to be called even with no peers")
	}
}

func TestRunMeshUpgrade_OfflinePeerSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	orig := runSelfUpgradeFunc
	t.Cleanup(func() { runSelfUpgradeFunc = orig })
	runSelfUpgradeFunc = func(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
		return nil
	}

	nodes := []daemon.MeshNode{
		{Name: "ctrl", Address: "127.0.0.1", Port: 8844, Roles: []string{"controller"}, Status: daemon.StatusOnline},
		{Name: "gone", Address: "127.0.0.1", Port: 9, Roles: []string{"store"}, Status: daemon.StatusOffline},
	}
	daemonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonSrv.Close()

	cfg := &meshconfig.Config{
		Name:      "ctrl",
		Port:      parsePort(t, daemonSrv.URL),
		Roles:     []string{"controller"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	oldOut := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := runMeshUpgrade(cfg, "1.0.0", true, false, true)

	w.Close()
	os.Stdout = oldOut

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	var result meshUpgradeOutput
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	byName := make(map[string]nodeUpgradeResult)
	for _, n := range result.Nodes {
		byName[n.Name] = n
	}
	if byName["gone"].Status != "skipped" {
		t.Errorf("offline node should be skipped, got %q", byName["gone"].Status)
	}
}

func TestCmdUpgrade_StandaloneWhenNoMesh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// No mesh config → standalone path. Stub selfupgrade to avoid real calls.
	orig := runSelfUpgradeFunc
	t.Cleanup(func() { runSelfUpgradeFunc = orig })
	runSelfUpgradeFunc = func(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
		return fmt.Errorf("stub: would do standalone upgrade to %s", targetVersion)
	}

	code := cmdUpgrade([]string{"--version", "1.0.0", "--yes"})
	// We expect exit 1 because the stub returns an error (proving standalone path was taken).
	if code != 1 {
		t.Fatalf("expected exit 1 from stub error, got %d", code)
	}
}

func TestCmdUpgrade_StandaloneWhenNotController(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &meshconfig.Config{
		Name:      "worker",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	orig := runSelfUpgradeFunc
	t.Cleanup(func() { runSelfUpgradeFunc = orig })
	runSelfUpgradeFunc = func(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
		return fmt.Errorf("stub: standalone upgrade to %s", targetVersion)
	}

	code := cmdUpgrade([]string{"--version", "1.0.0", "--yes"})
	// Expects exit 1 because stub returns error, confirming standalone path.
	if code != 1 {
		t.Fatalf("expected exit 1 from stub error, got %d", code)
	}
}

func TestUpgradePeerNode_MeshKeyForwarded(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		// Health poll: always return target version.
	}))
	defer srv.Close()

	// Start a health server that returns the target version immediately.
	peer := daemon.MeshNode{Name: "auth-peer", Address: "127.0.0.1", Port: parsePort(t, srv.URL)}

	// Create a second server for health polls that returns the target version.
	healthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"version": "3.0.0"})
	}))
	defer healthSrv.Close()

	// Use same server for both upgrade and health by overriding the peer port.
	// Since upgradePeerNode uses peer.Address:peer.Port for both /v1/upgrade and /v1/health,
	// we need a single server handling both paths.
	pollCount := 0
	combinedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch {
		case r.URL.Path == "/v1/upgrade":
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		case r.URL.Path == "/v1/health":
			pollCount++
			json.NewEncoder(w).Encode(map[string]string{"version": "3.0.0"})
		}
	}))
	defer combinedSrv.Close()

	peer.Port = parsePort(t, combinedSrv.URL)
	_ = time.Now() // avoid unused import
	r := upgradePeerNode(peer, "3.0.0", "secret-key")

	if r.Status != "upgraded" {
		t.Errorf("expected upgraded, got %q", r.Status)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("expected Authorization header 'Bearer secret-key', got %q", gotAuth)
	}
}

// TestCmdUpgrade_StandaloneAlreadyCurrent_NoRestart verifies that when the binary
// is already at the target version (ErrAlreadyCurrent), cmdUpgrade exits 0 and does
// NOT attempt to restart the service (fix for issue #216).
func TestCmdUpgrade_StandaloneAlreadyCurrent_NoRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	orig := runSelfUpgradeFunc
	t.Cleanup(func() { runSelfUpgradeFunc = orig })
	runSelfUpgradeFunc = func(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
		return selfupgrade.ErrAlreadyCurrent
	}

	// If the code mistakenly calls service.Restart(), it will fail because no service
	// is installed in the test environment — but we're primarily asserting exit code 0.
	code := cmdUpgrade([]string{"--version", "1.0.0", "--yes"})
	if code != 0 {
		t.Fatalf("expected exit 0 when already current, got %d", code)
	}
}

// TestRunMeshUpgrade_AlreadyCurrentPeersSkipped verifies that online peers already at
// the target version are shown as "already current" in the table and not sent a
// POST /v1/upgrade request (fix for issue #215).
func TestRunMeshUpgrade_AlreadyCurrentPeersSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	orig := runSelfUpgradeFunc
	t.Cleanup(func() { runSelfUpgradeFunc = orig })
	runSelfUpgradeFunc = func(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
		return selfupgrade.ErrAlreadyCurrent
	}

	upgradeHit := false
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/upgrade" {
			upgradeHit = true
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		}
	}))
	defer peerSrv.Close()

	nodes := []daemon.MeshNode{
		{Name: "ctrl", Address: "127.0.0.1", Port: 8844, Roles: []string{"controller"}, Status: daemon.StatusOnline, Version: "1.0.0"},
		{Name: "peer1", Address: "127.0.0.1", Port: parsePort(t, peerSrv.URL), Roles: []string{"store"}, Status: daemon.StatusOnline, Version: "1.0.0"},
	}
	daemonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonSrv.Close()

	cfg := &meshconfig.Config{
		Name:      "ctrl",
		Port:      parsePort(t, daemonSrv.URL),
		Roles:     []string{"controller"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	oldOut := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := runMeshUpgrade(cfg, "1.0.0", true, false, true)

	w.Close()
	os.Stdout = oldOut

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if upgradeHit {
		t.Error("POST /v1/upgrade should not be sent to peer already at target version")
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	var result meshUpgradeOutput
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}

	byName := make(map[string]nodeUpgradeResult)
	for _, n := range result.Nodes {
		byName[n.Name] = n
	}
	if byName["peer1"].Status != "already_current" {
		t.Errorf("peer1: expected 'already_current', got %q", byName["peer1"].Status)
	}
	if byName["ctrl"].Status != "already_current" {
		t.Errorf("ctrl: expected 'already_current', got %q", byName["ctrl"].Status)
	}
}

// TestRunMeshUpgrade_MixedCurrentAndNeedsUpgrade verifies that only peers NOT at the
// target version receive a POST /v1/upgrade, while already-current peers are skipped.
func TestRunMeshUpgrade_MixedCurrentAndNeedsUpgrade(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	orig := runSelfUpgradeFunc
	t.Cleanup(func() { runSelfUpgradeFunc = orig })
	runSelfUpgradeFunc = func(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
		return nil
	}

	var upgradeRequestsReceived []string
	var mu sync.Mutex
	peerOld := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/upgrade" && r.Method == http.MethodPost {
			mu.Lock()
			upgradeRequestsReceived = append(upgradeRequestsReceived, "old-peer")
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		}
		if r.URL.Path == "/v1/health" {
			json.NewEncoder(w).Encode(map[string]string{"version": "1.0.0"})
		}
	}))
	defer peerOld.Close()

	peerCurrent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/upgrade" {
			mu.Lock()
			upgradeRequestsReceived = append(upgradeRequestsReceived, "current-peer")
			mu.Unlock()
		}
	}))
	defer peerCurrent.Close()

	nodes := []daemon.MeshNode{
		{Name: "ctrl", Address: "127.0.0.1", Port: 8844, Roles: []string{"controller"}, Status: daemon.StatusOnline, Version: "0.9.0"},
		{Name: "old-peer", Address: "127.0.0.1", Port: parsePort(t, peerOld.URL), Roles: []string{"store"}, Status: daemon.StatusOnline, Version: "0.9.0"},
		{Name: "current-peer", Address: "127.0.0.1", Port: parsePort(t, peerCurrent.URL), Roles: []string{"store"}, Status: daemon.StatusOnline, Version: "1.0.0"},
	}
	daemonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			json.NewEncoder(w).Encode(nodes)
		}
	}))
	defer daemonSrv.Close()

	cfg := &meshconfig.Config{
		Name:      "ctrl",
		Port:      parsePort(t, daemonSrv.URL),
		Roles:     []string{"controller"},
		ShelfRoot: filepath.Join(home, "shelf"),
	}
	if err := meshconfig.WriteTo(meshconfig.ConfigPath(), cfg); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	oldOut := os.Stdout
	rp, wp, _ := os.Pipe()
	os.Stdout = wp

	code := runMeshUpgrade(cfg, "1.0.0", true, false, true)

	wp.Close()
	os.Stdout = oldOut

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	for _, name := range upgradeRequestsReceived {
		if name == "current-peer" {
			t.Error("POST /v1/upgrade should not be sent to current-peer already at 1.0.0")
		}
	}

	var buf bytes.Buffer
	buf.ReadFrom(rp)
	var result meshUpgradeOutput
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}
	byName := make(map[string]nodeUpgradeResult)
	for _, n := range result.Nodes {
		byName[n.Name] = n
	}
	if byName["current-peer"].Status != "already_current" {
		t.Errorf("current-peer: expected 'already_current', got %q", byName["current-peer"].Status)
	}
	if byName["old-peer"].Status != "upgraded" {
		t.Errorf("old-peer: expected 'upgraded', got %q", byName["old-peer"].Status)
	}
}

func parsePort(t *testing.T, rawURL string) int {
	t.Helper()
	// rawURL is like "http://127.0.0.1:PORT"
	portStr := rawURL[strings.LastIndex(rawURL, ":")+1:]
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parsePort(%q): %v", rawURL, err)
	}
	return port
}

// TestMeshUpgrade_ElapsedSecondsAlwaysPresent verifies that all nodes in the
// --json output include "elapsed_seconds", including already_current and skipped nodes.
func TestMeshUpgrade_ElapsedSecondsAlwaysPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Two nodes: one already_current peer, one offline peer.
	// The controller self-upgrade also returns already_current.
	target := "1.2.3"

	// Stub self-upgrade to return ErrAlreadyCurrent.
	orig := runSelfUpgradeFunc
	runSelfUpgradeFunc = func(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
		return selfupgrade.ErrAlreadyCurrent
	}
	t.Cleanup(func() { runSelfUpgradeFunc = orig })

	// Fake daemon returns two peer nodes: one online already_current, one offline.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/nodes":
			nodes := []daemon.MeshNode{
				{Name: "self", Address: "127.0.0.1", Port: 0, Status: daemon.StatusOnline, Version: target},
				{Name: "peer-current", Address: "127.0.0.1", Port: 0, Status: daemon.StatusOnline, Version: target},
				{Name: "peer-offline", Address: "127.0.0.1", Port: 0, Status: daemon.StatusOffline},
			}
			json.NewEncoder(w).Encode(nodes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	port := parsePort(t, srv.URL)

	meshCfgDir := filepath.Join(home, ".model-shelf")
	os.MkdirAll(meshCfgDir, 0o755)
	cfgContent := fmt.Sprintf(
		"name = \"self\"\nport = %d\nroles = [\"controller\",\"store\"]\nshelf_root = %q\n",
		port, filepath.Join(home, "shelf"),
	)
	os.WriteFile(filepath.Join(meshCfgDir, "config.toml"), []byte(cfgContent), 0o644)

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cfg, _ := meshconfig.Load()
	code := runMeshUpgrade(cfg, target, true, false, true)

	w.Close()
	os.Stdout = oldStdout
	outBytes, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput: %s", code, outBytes)
	}

	var out meshUpgradeOutput
	if err := json.Unmarshal(outBytes, &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nraw: %s", err, outBytes)
	}

	// Unmarshal into raw map to verify presence of "elapsed_seconds" key in every node.
	var rawOut struct {
		Nodes []map[string]interface{} `json:"nodes"`
	}
	if err := json.Unmarshal(outBytes, &rawOut); err != nil {
		t.Fatalf("failed to parse raw JSON: %v", err)
	}

	for _, node := range rawOut.Nodes {
		name, _ := node["name"].(string)
		if _, ok := node["elapsed_seconds"]; !ok {
			t.Errorf("node %q: expected 'elapsed_seconds' field in JSON output, but it was absent", name)
		}
	}

	// already_current and skipped nodes should have elapsed_seconds = 0.
	byName := make(map[string]map[string]interface{})
	for _, node := range rawOut.Nodes {
		name, _ := node["name"].(string)
		byName[name] = node
	}
	for _, nodeName := range []string{"self", "peer-current", "peer-offline"} {
		n, ok := byName[nodeName]
		if !ok {
			t.Errorf("node %q missing from output", nodeName)
			continue
		}
		elapsed, _ := n["elapsed_seconds"].(float64)
		if elapsed != 0 {
			t.Errorf("node %q: expected elapsed_seconds=0 for non-upgraded node, got %v", nodeName, elapsed)
		}
	}
}
