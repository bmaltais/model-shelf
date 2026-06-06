package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alexziskind1/model-shelf/internal/resolver"
)

// EvictionResult describes a single model evicted during a cascade.
type EvictionResult struct {
	RepoID      string `json:"repo_id"`
	Format      string `json:"format"`
	Quant       string `json:"quant,omitempty"`
	SizeBytes   int64  `json:"size_bytes"`
	Relocated   bool   `json:"relocated"`   // true if transferred to peer before deletion
	RelocationTarget string `json:"relocation_target,omitempty"` // peer name if relocated
}

// EvictionError is returned when eviction cannot free enough space.
type EvictionError struct {
	NeededBytes int64
	FreedBytes  int64
	Reason      string
}

func (e *EvictionError) Error() string {
	return fmt.Sprintf("eviction failed: need %d bytes but could only free %d bytes: %s",
		e.NeededBytes, e.FreedBytes, e.Reason)
}

// evictForSpace attempts to free at least neededBytes on the local node by evicting
// LRU models. Returns the list of evicted models, or an error if unable to free enough.
//
// Eviction rules:
//  1. Models are sorted by last-accessed timestamp (oldest first = LRU).
//  2. If the model exists on another node, delete locally.
//  3. If the model is the last copy in the mesh, transfer to the peer with most free space first.
//  4. If the model is the last copy and no peer has room, skip it (never lose a model).
//  5. Cascade: keep evicting until enough space is freed or no more candidates.
func (d *Daemon) evictForSpace(neededBytes int64, excludeRepoID string) ([]EvictionResult, error) {
	// Get current disk free space.
	_, freeGB := DiskUsage(d.cfg.ShelfRoot)
	freeBytes := int64(freeGB * 1024 * 1024 * 1024)

	if freeBytes >= neededBytes {
		return nil, nil // Already have enough space.
	}

	deficit := neededBytes - freeBytes

	// Get local inventory sorted by LRU (oldest last_accessed first).
	entries := d.inventory.Entries()
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastAccessed.Before(entries[j].LastAccessed)
	})

	// Query peer inventories to determine which models have copies elsewhere.
	peerInventories := d.collectPeerInventories()

	var results []EvictionResult
	var freedBytes int64

	for _, entry := range entries {
		if freedBytes >= deficit {
			break
		}

		// Never evict the model being pulled.
		if entry.RepoID == excludeRepoID {
			continue
		}

		existsOnPeer := modelExistsOnPeer(entry, peerInventories)

		if existsOnPeer {
			// Safe to delete locally — copies exist elsewhere.
			if err := d.deleteModel(entry); err != nil {
				log.Printf("eviction: failed to delete %s: %v", entry.Key(), err)
				continue
			}
			results = append(results, EvictionResult{
				RepoID:    entry.RepoID,
				Format:    entry.Format,
				Quant:     entry.Quant,
				SizeBytes: entry.SizeBytes,
				Relocated: false,
			})
			freedBytes += entry.SizeBytes
		} else {
			// Last copy — must relocate before deleting.
			peer := d.findPeerWithMostFreeSpace(entry.SizeBytes)
			if peer == nil {
				// No peer has room — skip this model to preserve it.
				log.Printf("eviction: skipping %s (last copy, no peer has room)", entry.Key())
				continue
			}

			// Transfer to peer first.
			if err := d.relocateModel(entry, peer); err != nil {
				log.Printf("eviction: failed to relocate %s to %s: %v", entry.Key(), peer.Name, err)
				continue
			}

			// Now delete locally.
			if err := d.deleteModel(entry); err != nil {
				log.Printf("eviction: failed to delete %s after relocation: %v", entry.Key(), err)
				continue
			}

			results = append(results, EvictionResult{
				RepoID:           entry.RepoID,
				Format:           entry.Format,
				Quant:            entry.Quant,
				SizeBytes:        entry.SizeBytes,
				Relocated:        true,
				RelocationTarget: peer.Name,
			})
			freedBytes += entry.SizeBytes
		}
	}

	if freedBytes < deficit {
		return results, &EvictionError{
			NeededBytes: deficit,
			FreedBytes:  freedBytes,
			Reason:      "remaining models are last-copy with no peer space to relocate",
		}
	}

	return results, nil
}

// peerInventoryInfo holds a peer's inventory and disk info.
type peerInventoryInfo struct {
	Name       string
	Address    string
	Port       int
	DiskFreeGB float64
	Entries    []InventoryEntry
}

// collectPeerInventories queries all online peers for their inventories.
func (d *Daemon) collectPeerInventories() []peerInventoryInfo {
	nodes := d.gossip.Nodes()
	meshKey := d.cfg.MeshKey
	var peers []peerInventoryInfo

	for _, node := range nodes {
		if node.Name == d.cfg.Name || node.Status == StatusOffline {
			continue
		}

		hostPort := net.JoinHostPort(node.Address, fmt.Sprintf("%d", node.Port))
		url := fmt.Sprintf("http://%s/v1/inventory", hostPort)
		client := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		if meshKey != "" {
			req.Header.Set("Authorization", "Bearer "+meshKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		var entries []InventoryEntry
		if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		peers = append(peers, peerInventoryInfo{
			Name:       node.Name,
			Address:    node.Address,
			Port:       node.Port,
			DiskFreeGB: node.DiskFreeGB,
			Entries:    entries,
		})
	}

	return peers
}

// modelExistsOnPeer checks if any peer has a copy of the model.
func modelExistsOnPeer(entry InventoryEntry, peers []peerInventoryInfo) bool {
	targetKey := entry.Key()
	for _, peer := range peers {
		for _, e := range peer.Entries {
			if e.Key() == targetKey {
				return true
			}
		}
	}
	return false
}

// findPeerWithMostFreeSpace finds the peer with the most free disk space that
// can accommodate the given model size.
func (d *Daemon) findPeerWithMostFreeSpace(neededBytes int64) *peerSource {
	nodes := d.gossip.Nodes()
	neededGB := float64(neededBytes) / (1024 * 1024 * 1024)

	var best *MeshNode
	for i := range nodes {
		n := &nodes[i]
		if n.Name == d.cfg.Name || n.Status == StatusOffline {
			continue
		}
		// Must be a Store or Executor (can hold models).
		if !HasRole(n.Roles, "store") && !HasRole(n.Roles, "executor") {
			continue
		}
		if n.DiskFreeGB < neededGB {
			continue
		}
		if best == nil || n.DiskFreeGB > best.DiskFreeGB {
			best = n
		}
	}

	if best == nil {
		return nil
	}
	return &peerSource{
		Name:    best.Name,
		Address: best.Address,
		Port:    best.Port,
	}
}

// relocateModel transfers a model to a peer by triggering a pull on the peer.
// The peer will pull from this node's download endpoint.
func (d *Daemon) relocateModel(entry InventoryEntry, peer *peerSource) error {
	hostPort := net.JoinHostPort(peer.Address, fmt.Sprintf("%d", peer.Port))
	url := fmt.Sprintf("http://%s/v1/pull", hostPort)

	reqBody := fmt.Sprintf(`{"repo_id":%q,"format":%q,"quant":%q}`,
		entry.RepoID, entry.Format, entry.Quant)

	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("creating relocation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if d.cfg.MeshKey != "" {
		req.Header.Set("Authorization", "Bearer "+d.cfg.MeshKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("requesting relocation to %s: %w", peer.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("peer %s rejected relocation pull: HTTP %d", peer.Name, resp.StatusCode)
	}

	// Wait for the peer to complete the transfer.
	var pullResp PullResponse
	if err := json.NewDecoder(resp.Body).Decode(&pullResp); err != nil {
		return fmt.Errorf("decoding relocation response: %w", err)
	}

	// Poll the peer's job status until complete or failed.
	return d.waitForPeerJob(peer, pullResp.JobID)
}

// waitForPeerJob polls a peer's job endpoint until the job reaches a terminal state.
func (d *Daemon) waitForPeerJob(peer *peerSource, jobID string) error {
	hostPort := net.JoinHostPort(peer.Address, fmt.Sprintf("%d", peer.Port))
	url := fmt.Sprintf("http://%s/v1/jobs?id=%s&local=true", hostPort, jobID)
	client := &http.Client{Timeout: 5 * time.Second}

	timeout := time.After(10 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timed out waiting for relocation job %s on %s", jobID, peer.Name)
		case <-ticker.C:
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				continue
			}
			if d.cfg.MeshKey != "" {
				req.Header.Set("Authorization", "Bearer "+d.cfg.MeshKey)
			}
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				continue
			}
			var job Job
			if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
				resp.Body.Close()
				continue
			}
			resp.Body.Close()

			switch job.Status {
			case JobCompleted, JobAlreadyPresent:
				return nil
			case JobFailed:
				return fmt.Errorf("relocation job %s failed on %s: %s", jobID, peer.Name, job.Error)
			}
			// Still in progress, continue polling.
		}
	}
}

// deleteModel removes a model from the local shelf and inventory.
func (d *Daemon) deleteModel(entry InventoryEntry) error {
	var modelPath string
	var err error

	if entry.Format == "gguf" {
		modelPath, err = resolver.ShelfPathGGUF(d.cfg.ShelfRoot, entry.RepoID, entry.Quant)
	} else {
		modelPath, err = resolver.ShelfPathSnapshot(d.cfg.ShelfRoot, entry.RepoID, entry.Format)
	}
	if err != nil {
		return fmt.Errorf("resolving shelf path for deletion: %w", err)
	}

	// Delete from disk.
	if err := os.RemoveAll(modelPath); err != nil {
		return fmt.Errorf("removing %s: %w", modelPath, err)
	}

	// Clean up empty parent directories.
	cleanEmptyParents(modelPath, d.cfg.ShelfRoot)

	// Remove from inventory.
	release, lockErr := acquireInventoryLock()
	if lockErr != nil {
		log.Printf("eviction: failed to acquire inventory lock: %v", lockErr)
	} else {
		d.inventory.Remove(entry.RepoID, entry.Format, entry.Quant)
		if err := d.inventory.Save(); err != nil {
			log.Printf("eviction: inventory save error: %v", err)
		}
		release()
	}

	log.Printf("eviction: deleted %s (format=%s, quant=%s) from local shelf", entry.RepoID, entry.Format, entry.Quant)
	return nil
}

// cleanEmptyParents removes empty parent directories up to (but not including) the shelf root.
func cleanEmptyParents(path, shelfRoot string) {
	dir := filepath.Dir(path)
	for dir != shelfRoot && dir != "." && dir != "/" {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			break
		}
		os.Remove(dir)
		dir = filepath.Dir(dir)
	}
}

// evictIfNeeded checks if there's enough disk space for a model and triggers eviction if not.
// It estimates the model size via the HF API and evicts LRU models as needed.
func (d *Daemon) evictIfNeeded(jobID, repoID, format, quant string) error {
	// Estimate model size.
	sizeBytes, err := queryModelSize(repoID, format, quant)
	if err != nil {
		// If we can't estimate size, proceed without eviction — download may still succeed.
		log.Printf("eviction: cannot estimate size for %s (format=%s, quant=%s): %v — skipping eviction check",
			repoID, format, quant, err)
		return nil
	}

	// Add 10% overhead for filesystem overhead.
	neededBytes := int64(float64(sizeBytes) * 1.1)

	// Check if eviction is needed before changing job status.
	_, freeGB := DiskUsage(d.cfg.ShelfRoot)
	freeBytes := int64(freeGB * 1024 * 1024 * 1024)
	if freeBytes >= neededBytes {
		return nil // Already have enough space.
	}

	d.jobs.SetEvicting(jobID)

	results, evictErr := d.evictForSpace(neededBytes, repoID)
	if len(results) > 0 {
		log.Printf("eviction: freed space by evicting %d model(s) for pull of %s (job=%s)", len(results), repoID, jobID)
		for _, r := range results {
			if r.Relocated {
				log.Printf("eviction:   %s (format=%s, quant=%s) relocated to %s", r.RepoID, r.Format, r.Quant, r.RelocationTarget)
			} else {
				log.Printf("eviction:   %s (format=%s, quant=%s) deleted (copies exist elsewhere)", r.RepoID, r.Format, r.Quant)
			}
		}
	}

	if evictErr != nil {
		return fmt.Errorf("insufficient disk space for %s: %v", repoID, evictErr)
	}
	return nil
}
