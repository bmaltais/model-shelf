package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

const (
	pollInterval     = 15 * time.Second
	offlineThreshold = 3
	eventPushTimeout = 5 * time.Second
)

// NodeStatus represents whether a node is online or offline.
type NodeStatus string

const (
	StatusOnline  NodeStatus = "online"
	StatusOffline NodeStatus = "offline"
)

// MeshNode is the full mesh state for a node (extends NodeInfo with status).
type MeshNode struct {
	Name          string     `json:"name"`
	Address       string     `json:"address"`
	Port          int        `json:"port"`
	Roles         []string   `json:"roles"`
	Status        NodeStatus `json:"status"`
	MissedPolls   int        `json:"missed_polls,omitempty"`
	DiskFreeGB    float64    `json:"disk_free_gb"`
	DiskTotalGB   float64    `json:"disk_total_gb"`
	UptimeSeconds float64    `json:"uptime_seconds"`
	GPU           *GPUInfo   `json:"gpu"`
	LastSeen      *time.Time `json:"last_seen"`
}

// EventType describes what happened.
type EventType string

const (
	EventJoin         EventType = "join"
	EventLeave        EventType = "leave"
	EventHealthChange EventType = "health_change"
)

// validEventType returns true if the event type is recognized.
func validEventType(t EventType) bool {
	switch t {
	case EventJoin, EventLeave, EventHealthChange:
		return true
	}
	return false
}

// Event is pushed between nodes to replicate state changes.
type Event struct {
	Type      EventType `json:"type"`
	Node      MeshNode  `json:"node"`
	Timestamp time.Time `json:"timestamp"`
}

// Gossip manages mesh state replication.
type Gossip struct {
	mu         sync.RWMutex
	nodes      []MeshNode
	self       string // this node's name
	shelfRoot  string // for self disk usage updates
	meshKey    string
	startTime  time.Time              // daemon start time for uptime calculation
	jobs       *JobStore              // shared job store for gossip replication
	gpuConfig  *meshconfig.GPUConfig  // GPU override config for self-refresh
	cancel     context.CancelFunc
}

// NewGossip creates a gossip instance, loading persisted state if available.
func NewGossip(selfNode MeshNode, meshKey string, shelfRoot string, startTime time.Time, jobs *JobStore) *Gossip {
	g := &Gossip{
		self:      selfNode.Name,
		shelfRoot: shelfRoot,
		meshKey:   meshKey,
		startTime: startTime,
		jobs:      jobs,
	}

	// Try to load persisted state.
	persisted, err := LoadMeshState()
	if err != nil {
		log.Printf("gossip: failed to load persisted state: %v (starting fresh)", err)
	}
	if len(persisted) > 0 {
		// Ensure self is present and online.
		found := false
		for i := range persisted {
			if persisted[i].Name == selfNode.Name {
				persisted[i] = selfNode
				persisted[i].Status = StatusOnline
				found = true
				break
			}
		}
		if !found {
			persisted = append([]MeshNode{selfNode}, persisted...)
		}
		g.nodes = deduplicateByAddress(persisted)
	} else {
		g.nodes = []MeshNode{selfNode}
	}

	return g
}

// SetGPUConfig sets the GPU override config for self-node refresh.
func (g *Gossip) SetGPUConfig(cfg *meshconfig.GPUConfig) {
	g.mu.Lock()
	g.gpuConfig = cfg
	g.mu.Unlock()
}

// Nodes returns a copy of the current mesh state with fresh self-node metrics.
func (g *Gossip) Nodes() []MeshNode {
	// Refresh self-node metrics so callers always get current disk/uptime/GPU.
	if g.shelfRoot != "" {
		totalGB, freeGB := DiskUsage(g.shelfRoot)
		now := time.Now()
		g.mu.Lock()
		for i := range g.nodes {
			if g.nodes[i].Name == g.self {
				// Only update disk metrics if DiskUsage returned valid data;
				// avoids overwriting known values with zeros on transient failures.
				if totalGB > 0 {
					g.nodes[i].DiskFreeGB = freeGB
					g.nodes[i].DiskTotalGB = totalGB
				}
				g.nodes[i].UptimeSeconds = time.Since(g.startTime).Seconds()
				g.nodes[i].GPU = RefreshGPUAvailableVRAM(g.nodes[i].GPU, g.gpuConfig)
				g.nodes[i].LastSeen = &now
				break
			}
		}
		g.mu.Unlock()
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]MeshNode, len(g.nodes))
	copy(out, g.nodes)
	return out
}

// evictByAddressLocked removes any node with the same address:port but a
// different name. This handles node renames — when a machine re-announces
// under a new name, the stale entry is evicted. Must be called with mu held.
func (g *Gossip) evictByAddressLocked(name, address string, port int) {
	if address == "" {
		return
	}
	filtered := g.nodes[:0]
	for _, n := range g.nodes {
		if n.Address == address && n.Port == port && n.Name != name {
			log.Printf("gossip: evicting stale node %q (same address %s:%d as %q)", n.Name, address, port, name)
			continue
		}
		filtered = append(filtered, n)
	}
	g.nodes = filtered
}

// deduplicateByAddress removes duplicate address:port entries from a node list,
// keeping the last occurrence (which is the most recently added/updated entry).
// Used at load time and when setting nodes wholesale to prevent persisted ghosts.
func deduplicateByAddress(nodes []MeshNode) []MeshNode {
	type addrKey struct {
		address string
		port    int
	}
	// Walk in reverse so the last entry for each address wins.
	seen := make(map[addrKey]bool, len(nodes))
	// First pass: mark which indices to keep (last occurrence per address).
	keep := make([]bool, len(nodes))
	for i := len(nodes) - 1; i >= 0; i-- {
		if nodes[i].Address == "" {
			keep[i] = true
			continue
		}
		k := addrKey{nodes[i].Address, nodes[i].Port}
		if !seen[k] {
			seen[k] = true
			keep[i] = true
		}
	}
	result := make([]MeshNode, 0, len(nodes))
	for i, n := range nodes {
		if keep[i] {
			result = append(result, n)
		}
	}
	return result
}

// AddNode adds or updates a node and pushes a join event to peers.
// If another node with the same address:port exists under a different name,
// the old entry is evicted (handles node renames).
func (g *Gossip) AddNode(node MeshNode) {
	g.mu.Lock()
	g.evictByAddressLocked(node.Name, node.Address, node.Port)
	found := false
	for i := range g.nodes {
		if g.nodes[i].Name == node.Name {
			g.nodes[i] = node
			found = true
			break
		}
	}
	if !found {
		g.nodes = append(g.nodes, node)
	}
	nodes := make([]MeshNode, len(g.nodes))
	copy(nodes, g.nodes)
	g.mu.Unlock()

	g.persist()
	g.pushEvent(Event{
		Type:      EventJoin,
		Node:      node,
		Timestamp: time.Now(),
	}, nodes)
}

// RemoveNode marks a node as left and pushes a leave event.
func (g *Gossip) RemoveNode(name string) {
	g.mu.Lock()
	var removed MeshNode
	newNodes := g.nodes[:0]
	for _, n := range g.nodes {
		if n.Name == name {
			removed = n
			continue
		}
		newNodes = append(newNodes, n)
	}
	g.nodes = newNodes
	nodes := make([]MeshNode, len(g.nodes))
	copy(nodes, g.nodes)
	g.mu.Unlock()

	if removed.Name != "" {
		g.persist()
		g.pushEvent(Event{
			Type:      EventLeave,
			Node:      removed,
			Timestamp: time.Now(),
		}, nodes)
	}
}

// ApplyEvent processes an incoming event from a peer.
func (g *Gossip) ApplyEvent(ev Event) {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch ev.Type {
	case EventJoin, EventHealthChange:
		g.evictByAddressLocked(ev.Node.Name, ev.Node.Address, ev.Node.Port)
		found := false
		for i := range g.nodes {
			if g.nodes[i].Name == ev.Node.Name {
				g.nodes[i] = ev.Node
				found = true
				break
			}
		}
		if !found {
			g.nodes = append(g.nodes, ev.Node)
		}
	case EventLeave:
		newNodes := g.nodes[:0]
		for _, n := range g.nodes {
			if n.Name == ev.Node.Name {
				continue
			}
			newNodes = append(newNodes, n)
		}
		g.nodes = newNodes
	}

	g.persistLocked()
}

// SetNodes sets the full mesh state (used when bootstrapping from join response).
func (g *Gossip) SetNodes(nodes []MeshNode) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes = deduplicateByAddress(nodes)
	g.persistLocked()
}

// StartPoller begins the background health poll loop.
func (g *Gossip) StartPoller(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	g.mu.Lock()
	g.cancel = cancel
	g.mu.Unlock()

	go g.pollLoop(ctx)
}

// Stop cancels the background poller.
func (g *Gossip) Stop() {
	g.mu.Lock()
	cancel := g.cancel
	g.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (g *Gossip) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.pollPeers()
		}
	}
}

func (g *Gossip) pollPeers() {
	// Update self disk metrics and last_seen first so it's included in the snapshot.
	if g.shelfRoot != "" {
		totalGB, freeGB := DiskUsage(g.shelfRoot)
		now := time.Now()
		g.mu.Lock()
		for i := range g.nodes {
			if g.nodes[i].Name == g.self {
				g.nodes[i].DiskFreeGB = freeGB
				g.nodes[i].DiskTotalGB = totalGB
				g.nodes[i].LastSeen = &now
				break
			}
		}
		g.mu.Unlock()
	}

	g.mu.RLock()
	nodes := make([]MeshNode, len(g.nodes))
	copy(nodes, g.nodes)
	g.mu.RUnlock()

	var changed bool
	var transitioned []MeshNode // nodes that changed status or metrics (for gossip push)
	for i := range nodes {
		if nodes[i].Name == g.self {
			continue
		}
		hr := g.checkHealth(nodes[i])
		if hr.OK {
			// Fetch remote jobs and merge into local store.
			g.fetchPeerJobs(nodes[i])

			now := time.Now()
			nodes[i].LastSeen = &now
			if nodes[i].Status == StatusOffline || nodes[i].MissedPolls > 0 {
				nodes[i].Status = StatusOnline
				nodes[i].MissedPolls = 0
				changed = true
				transitioned = append(transitioned, nodes[i])
				log.Printf("gossip: node %q is back online", nodes[i].Name)
			}
			metricsChanged := false
			if hr.DiskFreeGB > 0 && nodes[i].DiskFreeGB != hr.DiskFreeGB {
				nodes[i].DiskFreeGB = hr.DiskFreeGB
				metricsChanged = true
			}
			if hr.DiskTotalGB > 0 && nodes[i].DiskTotalGB != hr.DiskTotalGB {
				nodes[i].DiskTotalGB = hr.DiskTotalGB
				metricsChanged = true
			}
			// Update roles from health response so role changes propagate via
			// gossip polling without requiring a full re-join.
			if len(hr.Roles) > 0 && !rolesEqual(nodes[i].Roles, hr.Roles) {
				log.Printf("gossip: node %q roles changed: %v → %v", nodes[i].Name, nodes[i].Roles, hr.Roles)
				nodes[i].Roles = hr.Roles
				metricsChanged = true
			}
			// Update GPU info from peer health response (including nil to clear stale data).
			if hr.GPU != nodes[i].GPU {
				gpuChanged := false
				if hr.GPU == nil && nodes[i].GPU != nil {
					gpuChanged = true
				} else if hr.GPU != nil && nodes[i].GPU == nil {
					gpuChanged = true
				} else if hr.GPU != nil && nodes[i].GPU != nil &&
					(hr.GPU.Name != nodes[i].GPU.Name ||
						hr.GPU.VRAMTotalGB != nodes[i].GPU.VRAMTotalGB ||
						hr.GPU.VRAMAvailableGB != nodes[i].GPU.VRAMAvailableGB) {
					gpuChanged = true
				}
				nodes[i].GPU = hr.GPU
				if gpuChanged {
					metricsChanged = true
				}
			}
			// Update uptime locally but don't trigger event broadcast —
			// uptime always changes and would cause gossip spam every poll.
			if hr.UptimeSeconds > 0 {
				nodes[i].UptimeSeconds = hr.UptimeSeconds
				changed = true
			}
			if metricsChanged {
				changed = true
				// Broadcast updated metrics if not already in transition list.
				alreadyTransitioned := false
				for _, t := range transitioned {
					if t.Name == nodes[i].Name {
						alreadyTransitioned = true
						break
					}
				}
				if !alreadyTransitioned {
					transitioned = append(transitioned, nodes[i])
				}
			}
		} else {
			if nodes[i].MissedPolls < offlineThreshold {
				nodes[i].MissedPolls++
				changed = true
			}
			if nodes[i].MissedPolls >= offlineThreshold && nodes[i].Status != StatusOffline {
				nodes[i].Status = StatusOffline
				changed = true
				transitioned = append(transitioned, nodes[i])
				log.Printf("gossip: node %q marked offline (missed %d polls)", nodes[i].Name, nodes[i].MissedPolls)
			}
		}
	}

	if changed {
		g.mu.Lock()
		for _, updated := range nodes {
			for j := range g.nodes {
				if g.nodes[j].Name == updated.Name {
					g.nodes[j].Status = updated.Status
					g.nodes[j].MissedPolls = updated.MissedPolls
					g.nodes[j].DiskFreeGB = updated.DiskFreeGB
					g.nodes[j].DiskTotalGB = updated.DiskTotalGB
					g.nodes[j].UptimeSeconds = updated.UptimeSeconds
					g.nodes[j].GPU = updated.GPU
					g.nodes[j].LastSeen = updated.LastSeen
					g.nodes[j].Roles = updated.Roles
					break
				}
			}
		}
		allNodes := make([]MeshNode, len(g.nodes))
		copy(allNodes, g.nodes)
		g.persistLocked()
		g.mu.Unlock()

		// Push health change events for nodes that transitioned status or metrics.
		for _, node := range transitioned {
			g.pushEvent(Event{
				Type:      EventHealthChange,
				Node:      node,
				Timestamp: time.Now(),
			}, allNodes)
		}
	}
}

// healthResult holds the outcome of a health check.
type healthResult struct {
	OK            bool
	Roles         []string
	DiskFreeGB    float64
	DiskTotalGB   float64
	UptimeSeconds float64
	GPU           *GPUInfo
}

func (g *Gossip) checkHealth(node MeshNode) healthResult {
	url := fmt.Sprintf("http://%s:%d/v1/health", node.Address, node.Port)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return healthResult{}
	}
	if g.meshKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.meshKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return healthResult{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return healthResult{}
	}
	var hr struct {
		Roles         []string `json:"roles"`
		DiskFreeGB    float64  `json:"disk_free_gb"`
		DiskTotalGB   float64  `json:"disk_total_gb"`
		UptimeSeconds float64  `json:"uptime_seconds"`
		GPU           *GPUInfo `json:"gpu"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return healthResult{OK: true}
	}
	return healthResult{OK: true, Roles: hr.Roles, DiskFreeGB: hr.DiskFreeGB, DiskTotalGB: hr.DiskTotalGB, UptimeSeconds: hr.UptimeSeconds, GPU: hr.GPU}
}

// fetchPeerJobs fetches jobs from a peer node and merges them into the local store.
func (g *Gossip) fetchPeerJobs(node MeshNode) {
	if g.jobs == nil {
		return
	}
	url := fmt.Sprintf("http://%s:%d/v1/jobs", node.Address, node.Port)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return
	}
	if g.meshKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.meshKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var jobs []Job
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return
	}
	g.jobs.Merge(jobs)
}

func (g *Gossip) pushEvent(ev Event, nodes []MeshNode) {
	data, err := json.Marshal(ev)
	if err != nil {
		log.Printf("gossip: failed to marshal event: %v", err)
		return
	}

	for _, node := range nodes {
		if node.Name == g.self {
			continue
		}
		go g.sendEvent(node, data)
	}
}

func (g *Gossip) sendEvent(node MeshNode, data []byte) {
	url := fmt.Sprintf("http://%s:%d/v1/events", node.Address, node.Port)
	client := &http.Client{Timeout: eventPushTimeout}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if g.meshKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.meshKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("gossip: failed to push event to %s: %v", node.Name, err)
		return
	}
	resp.Body.Close()
}

func (g *Gossip) persist() {
	g.mu.RLock()
	nodes := make([]MeshNode, len(g.nodes))
	copy(nodes, g.nodes)
	g.mu.RUnlock()

	if err := SaveMeshState(nodes); err != nil {
		log.Printf("gossip: failed to persist state: %v", err)
	}
}

func (g *Gossip) persistLocked() {
	nodes := make([]MeshNode, len(g.nodes))
	copy(nodes, g.nodes)

	if err := SaveMeshState(nodes); err != nil {
		log.Printf("gossip: failed to persist state: %v", err)
	}
}

// rolesEqual returns true if two role slices contain the same roles (order-insensitive).
func rolesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	freq := make(map[string]int, len(a))
	for _, r := range a {
		freq[r]++
	}
	for _, r := range b {
		freq[r]--
		if freq[r] < 0 {
			return false
		}
	}
	return true
}

// BootstrapFromSeeds contacts seed nodes to fetch the full mesh state
// and merges it into local gossip state. This ensures non-controller nodes
// can always discover all mesh participants even if mesh.json is stale.

func (g *Gossip) BootstrapFromSeeds(seeds []string) {
	if len(seeds) == 0 {
		return
	}

	for _, seed := range seeds {
		nodes, err := g.fetchNodesFromSeed(seed)
		if err != nil {
			log.Printf("gossip: seed bootstrap from %s failed: %v", seed, err)
			continue
		}
		if len(nodes) == 0 {
			continue
		}

		g.mu.Lock()
		for _, remote := range nodes {
			if remote.Name == g.self {
				continue
			}
			// Merge: add or update nodes from seed response.
			found := false
			for i := range g.nodes {
				if g.nodes[i].Name == remote.Name {
					// Update address/port/roles from seed (authoritative).
					g.nodes[i].Address = remote.Address
					g.nodes[i].Port = remote.Port
					g.nodes[i].Roles = remote.Roles
					if remote.DiskFreeGB > 0 {
						g.nodes[i].DiskFreeGB = remote.DiskFreeGB
					}
					if remote.DiskTotalGB > 0 {
						g.nodes[i].DiskTotalGB = remote.DiskTotalGB
					}
					g.nodes[i].GPU = remote.GPU
					g.nodes[i].Status = remote.Status
					found = true
					break
				}
			}
			if !found {
				g.nodes = append(g.nodes, remote)
			}
		}
		g.nodes = deduplicateByAddress(g.nodes)
		g.persistLocked()
		g.mu.Unlock()

		log.Printf("gossip: bootstrapped %d node(s) from seed %s", len(nodes), seed)
		return // Success from one seed is enough.
	}
}

// fetchNodesFromSeed queries a seed node's /v1/nodes endpoint.
func (g *Gossip) fetchNodesFromSeed(seed string) ([]MeshNode, error) {
	url := fmt.Sprintf("http://%s/v1/nodes", seed)
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if g.meshKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.meshKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("seed returned HTTP %d", resp.StatusCode)
	}
	var nodes []MeshNode
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}
