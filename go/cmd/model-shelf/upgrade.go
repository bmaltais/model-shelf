package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
	"github.com/alexziskind1/model-shelf/internal/selfupgrade"
	"github.com/alexziskind1/model-shelf/internal/service"
)

// nodeUpgradeResult tracks the outcome of upgrading a single node.
type nodeUpgradeResult struct {
	Name           string  `json:"name"`
	Status         string  `json:"status"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	Reason         string  `json:"reason,omitempty"`
}

// meshUpgradeOutput is the --json output for mesh upgrades.
type meshUpgradeOutput struct {
	TargetVersion string              `json:"target_version"`
	Nodes         []nodeUpgradeResult `json:"nodes"`
}

// runSelfUpgradeFunc is a function variable so tests can stub the controller
// self-upgrade without making real network calls.
var runSelfUpgradeFunc = selfupgrade.Run

func cmdUpgrade(args []string) int {
	_, flags := parseFlags(args)

	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf upgrade [--version x.y.z] [--yes] [--force] [--json]")
		fmt.Println()
		fmt.Println("Fetch the latest release from GitHub, verify its SHA256 checksum,")
		fmt.Println("and atomically replace the running binary.")
		fmt.Println()
		fmt.Println("When run on a Controller node in a mesh, fans out the upgrade to all")
		fmt.Println("reachable peers first, polls for health confirmation, then self-upgrades last.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --version <x.y.z>  Pin upgrade to a specific release (default: latest)")
		fmt.Println("  --yes              Skip confirmation prompt")
		fmt.Println("  --force            Proceed even if already running the target version")
		fmt.Println("  --json             Emit structured JSON result (mesh mode only)")
		return 0
	}

	targetVersion := flags["version"]
	yes := flags["yes"] == "true"
	force := flags["force"] == "true"
	jsonOutput := flags["json"] == "true"

	// Mesh mode: controller in a configured mesh fans out the upgrade.
	if meshconfig.Exists() {
		cfg, err := meshconfig.Load()
		if err == nil && isControllerNode(cfg) {
			return runMeshUpgrade(cfg, targetVersion, yes, force, jsonOutput)
		}
	}

	// Standalone mode.
	err := runSelfUpgradeFunc(targetVersion, version, yes, force, os.Stdout, os.Stderr, os.Stdin)
	if errors.Is(err, selfupgrade.ErrAlreadyCurrent) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	status, statusErr := service.GetStatus()
	if statusErr == nil && status.Installed {
		if refreshErr := service.RefreshUnit(); refreshErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not refresh unit file: %v\n", refreshErr)
		}
		if restartErr := service.Restart(); restartErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not restart service: %v\n", restartErr)
			fmt.Fprintf(os.Stderr, "  Run 'model-shelf service restart' to apply the upgrade.\n")
		} else {
			fmt.Println("Service restarted successfully.")
		}
	} else {
		fmt.Println()
		fmt.Println("Reminder: restart the daemon to apply the upgrade:")
		fmt.Println("  model-shelf service restart")
		fmt.Println("  — or —")
		fmt.Println("  model-shelf daemon")
	}

	return 0
}

func isControllerNode(cfg *meshconfig.Config) bool {
	for _, r := range cfg.Roles {
		if r == "controller" {
			return true
		}
	}
	return false
}

// runMeshUpgrade orchestrates a controller-driven mesh upgrade:
// fans out to all reachable peers in parallel, polls for health, then self-upgrades last.
func runMeshUpgrade(cfg *meshconfig.Config, targetVersion string, yes, force, jsonOutput bool) int {
	stdout := io.Writer(os.Stdout)
	if jsonOutput {
		stdout = io.Discard
	}

	// 1. Resolve target version.
	if targetVersion == "" {
		fmt.Fprintln(stdout, "Fetching latest release...")
		v, err := selfupgrade.FetchLatestVersion()
		if err != nil {
			emitError(jsonOutput, fmt.Sprintf("could not determine latest version: %v", err))
			return 1
		}
		targetVersion = v
	}
	target := strings.TrimPrefix(targetVersion, "v")

	// 2. Fetch nodes from local daemon.
	nodes, err := fetchNodes(cfg)
	if err != nil {
		emitError(jsonOutput, err.Error())
		return 1
	}

	// Partition nodes: skip self, classify peers as needing upgrade, already current, or offline.
	var needsUpgrade, alreadyCurrent, offlinePeers []daemon.MeshNode
	for _, n := range nodes {
		if n.Name == cfg.Name {
			continue
		}
		if n.Status != daemon.StatusOnline {
			offlinePeers = append(offlinePeers, n)
			continue
		}
		if strings.TrimPrefix(n.Version, "v") == target {
			alreadyCurrent = append(alreadyCurrent, n)
		} else {
			needsUpgrade = append(needsUpgrade, n)
		}
	}

	// With --force, promote already-current peers into the upgrade set.
	// Track them separately so the confirmation table can show the right label.
	var forceUpgrade []daemon.MeshNode
	if force {
		forceUpgrade = alreadyCurrent
		needsUpgrade = append(needsUpgrade, alreadyCurrent...)
		alreadyCurrent = nil
	}

	// 3. Print confirmation table.
	nameWidth := 6 // minimum column width; computed from peers only
	if !jsonOutput {
		for _, p := range needsUpgrade {
			if len(p.Name) > nameWidth {
				nameWidth = len(p.Name)
			}
		}
		for _, p := range alreadyCurrent {
			if len(p.Name) > nameWidth {
				nameWidth = len(p.Name)
			}
		}
		for _, p := range offlinePeers {
			if len(p.Name) > nameWidth {
				nameWidth = len(p.Name)
			}
		}

		forceSet := make(map[string]bool, len(forceUpgrade))
		for _, p := range forceUpgrade {
			forceSet[p.Name] = true
		}
		for _, p := range needsUpgrade {
			if forceSet[p.Name] {
				fmt.Fprintf(stdout, "  %-*s  %s → %s   will upgrade (force)\n", nameWidth, p.Name, nodeVersion(p.Version), target)
			} else {
				fmt.Fprintf(stdout, "  %-*s  %s → %s   will upgrade\n", nameWidth, p.Name, nodeVersion(p.Version), target)
			}
		}
		for _, p := range alreadyCurrent {
			fmt.Fprintf(stdout, "  %-*s  %s            already current — skip\n", nameWidth, p.Name, nodeVersion(p.Version))
		}
		for _, p := range offlinePeers {
			fmt.Fprintf(stdout, "  %-*s  %s            offline — will skip\n", nameWidth, p.Name, nodeVersion(p.Version))
		}
		fmt.Fprintln(stdout)

		if !yes && len(needsUpgrade) > 0 {
			fmt.Fprintf(stdout, "Upgrade %d node(s) to v%s? [y/N] ", len(needsUpgrade), target)
		}
	}

	// Prompt unless --yes or no peers need upgrading.
	if !yes && len(needsUpgrade) > 0 && !jsonOutput {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(stdout, "Upgrade cancelled.")
			return 0
		}
	}

	// 4. Fan out upgrades to peers that need it in parallel.
	var resultsMu sync.Mutex
	results := make([]nodeUpgradeResult, 0, len(offlinePeers)+len(alreadyCurrent)+len(needsUpgrade)+1) // +1 for self

	// Pre-populate offline skips.
	for _, p := range offlinePeers {
		results = append(results, nodeUpgradeResult{Name: p.Name, Status: "skipped", Reason: "offline"})
		if !jsonOutput {
			fmt.Fprintf(stdout, "  ✗ %-*s  offline — skipped\n", nameWidth, p.Name)
		}
	}

	// Pre-populate already-current skips.
	for _, p := range alreadyCurrent {
		results = append(results, nodeUpgradeResult{Name: p.Name, Status: "already_current"})
		if !jsonOutput {
			fmt.Fprintf(stdout, "  – %-*s  already current\n", nameWidth, p.Name)
		}
	}

	var wg sync.WaitGroup
	for _, peer := range needsUpgrade {
		wg.Add(1)
		go func(p daemon.MeshNode, w int) {
			defer wg.Done()
			start := time.Now()
			r := upgradePeerNode(p, target, cfg.MeshKey, force)
			r.ElapsedSeconds = time.Since(start).Seconds()
			if !jsonOutput {
				if r.Status == "upgraded" {
					fmt.Fprintf(stdout, "  ✓ %-*s  upgraded → %s  (%.0fs)\n", w, p.Name, target, r.ElapsedSeconds)
				} else {
					fmt.Fprintf(stdout, "  ✗ %-*s  %s — %s\n", w, p.Name, r.Status, r.Reason)
				}
			}
			resultsMu.Lock()
			results = append(results, r)
			resultsMu.Unlock()
		}(peer, nameWidth)
	}
	wg.Wait()

	// 5. Self-upgrade controller last.
	fmt.Fprintln(stdout)
	selfStart := time.Now()
	selfResult := nodeUpgradeResult{Name: cfg.Name}

	selfErr := runSelfUpgradeFunc(target, version, true, force, stdout, os.Stderr, nil)
	selfResult.ElapsedSeconds = time.Since(selfStart).Seconds()
	if errors.Is(selfErr, selfupgrade.ErrAlreadyCurrent) {
		selfResult.Status = "already_current"
		selfResult.ElapsedSeconds = 0 // not meaningful for already_current; zero for schema consistency
		if !jsonOutput {
			fmt.Fprintf(stdout, "  – %-*s  already current\n", nameWidth, cfg.Name)
		}
		selfErr = nil
	} else if selfErr != nil {
		selfResult.Status = "failed"
		selfResult.Reason = selfErr.Error()
		fmt.Fprintf(os.Stderr, "error: self-upgrade failed: %v\n", selfErr)
	} else {
		selfResult.Status = "upgraded"
		if !jsonOutput {
			fmt.Fprintf(stdout, "  ✓ %-*s  upgraded → %s  (%.0fs)\n", nameWidth, cfg.Name, target, selfResult.ElapsedSeconds)
		}
		// Restart service after successful self-upgrade (binary was replaced).
		status, statusErr := service.GetStatus()
		if statusErr == nil && status.Installed {
			if refreshErr := service.RefreshUnit(); refreshErr != nil {
				fmt.Fprintf(os.Stderr, "warning: could not refresh unit file: %v\n", refreshErr)
			}
			if restartErr := service.Restart(); restartErr != nil {
				fmt.Fprintf(os.Stderr, "warning: could not restart service: %v\n", restartErr)
			} else {
				fmt.Fprintln(stdout, "Service restarted successfully.")
			}
		}
	}
	results = append(results, selfResult)

	if jsonOutput {
		out := meshUpgradeOutput{
			TargetVersion: target,
			Nodes:         results,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out) //nolint:errcheck
	}

	if selfErr != nil {
		return 1
	}
	return 0
}

// peerUpgradeRequest is the JSON body sent to POST /v1/upgrade on a peer node.
type peerUpgradeRequest struct {
	Version string `json:"version"`
	Force   bool   `json:"force,omitempty"`
}

// upgradePeerNode sends an upgrade request to a peer and polls health until the
// peer reports the new version or the 60s deadline expires.
// force is forwarded to every peer in the needsUpgrade list; for version-mismatched
// peers the daemon's already_current short-circuit never fires anyway.
func upgradePeerNode(peer daemon.MeshNode, target, meshKey string, force bool) nodeUpgradeResult {
	target = strings.TrimPrefix(target, "v")

	body, err := json.Marshal(peerUpgradeRequest{Version: target, Force: force})
	if err != nil {
		return nodeUpgradeResult{Name: peer.Name, Status: "failed", Reason: fmt.Sprintf("marshal request: %v", err)}
	}
	upgradeURL := fmt.Sprintf("http://%s:%d/v1/upgrade", peer.Address, peer.Port)
	req, err := http.NewRequest(http.MethodPost, upgradeURL, bytes.NewReader(body))
	if err != nil {
		return nodeUpgradeResult{Name: peer.Name, Status: "failed", Reason: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	if meshKey != "" {
		req.Header.Set("Authorization", "Bearer "+meshKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nodeUpgradeResult{Name: peer.Name, Status: "failed", Reason: fmt.Sprintf("upgrade request: %v", err)}
	}
	defer resp.Body.Close()

	// A 200 response means already at target version.
	if resp.StatusCode == http.StatusOK {
		var ur struct {
			Status string `json:"status"`
		}
		json.NewDecoder(resp.Body).Decode(&ur) //nolint:errcheck
		if ur.Status == "already_current" {
			return nodeUpgradeResult{Name: peer.Name, Status: "upgraded"}
		}
		return nodeUpgradeResult{Name: peer.Name, Status: "failed", Reason: fmt.Sprintf("unexpected 200 status: %s", ur.Status)}
	}

	if resp.StatusCode != http.StatusAccepted {
		return nodeUpgradeResult{Name: peer.Name, Status: "failed", Reason: fmt.Sprintf("upgrade returned HTTP %d", resp.StatusCode)}
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	// Poll health until peer reports the new version or deadline.
	// Reuse a single http.Client across all poll iterations to amortize connection setup.
	pollClient := &http.Client{Timeout: 3 * time.Second}
	healthURL := fmt.Sprintf("http://%s:%d/v1/health", peer.Address, peer.Port)
	deadline := time.Now().Add(60 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ver, ok := fetchPeerVersion(healthURL, meshKey, pollClient)
			if ok && strings.TrimPrefix(ver, "v") == target {
				return nodeUpgradeResult{Name: peer.Name, Status: "upgraded"}
			}
		case <-time.After(time.Until(deadline)):
			return nodeUpgradeResult{Name: peer.Name, Status: "failed", Reason: "did not report new version within 60s"}
		}
	}
}

// fetchPeerVersion queries a node's /v1/health and returns its reported version.
func fetchPeerVersion(url, meshKey string, client *http.Client) (string, bool) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	if meshKey != "" {
		req.Header.Set("Authorization", "Bearer "+meshKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var hr struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return "", false
	}
	return hr.Version, true
}

// nodeVersion returns a display string for a node's current version.
func nodeVersion(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}
