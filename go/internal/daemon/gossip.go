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
)

const (
	pollInterval     = 60 * time.Second
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
	Name        string     `json:"name"`
	Address     string     `json:"address"`
	Port        int        `json:"port"`
	Roles       []string   `json:"roles"`
	Status      NodeStatus `json:"status"`
	MissedPolls int        `json:"missed_polls,omitempty"`
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
	mu      sync.RWMutex
	nodes   []MeshNode
	self    string // this node's name
	meshKey string
	cancel  context.CancelFunc
}

// NewGossip creates a gossip instance, loading persisted state if available.
func NewGossip(selfNode MeshNode, meshKey string) *Gossip {
	g := &Gossip{
		self:    selfNode.Name,
		meshKey: meshKey,
	}

	// Try to load persisted state.
	persisted, err := loadMeshState()
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
		g.nodes = persisted
	} else {
		g.nodes = []MeshNode{selfNode}
	}

	return g
}

// Nodes returns a copy of the current mesh state.
func (g *Gossip) Nodes() []MeshNode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]MeshNode, len(g.nodes))
	copy(out, g.nodes)
	return out
}

// AddNode adds or updates a node and pushes a join event to peers.
func (g *Gossip) AddNode(node MeshNode) {
	g.mu.Lock()
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
	g.nodes = nodes
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
	g.mu.RLock()
	nodes := make([]MeshNode, len(g.nodes))
	copy(nodes, g.nodes)
	g.mu.RUnlock()

	var changed bool
	var transitioned []MeshNode // nodes that changed status (for gossip push)
	for i := range nodes {
		if nodes[i].Name == g.self {
			continue
		}
		if g.checkHealth(nodes[i]) {
			if nodes[i].Status == StatusOffline || nodes[i].MissedPolls > 0 {
				nodes[i].Status = StatusOnline
				nodes[i].MissedPolls = 0
				changed = true
				transitioned = append(transitioned, nodes[i])
				log.Printf("gossip: node %q is back online", nodes[i].Name)
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
					break
				}
			}
		}
		allNodes := make([]MeshNode, len(g.nodes))
		copy(allNodes, g.nodes)
		g.persistLocked()
		g.mu.Unlock()

		// Push health change events for nodes that transitioned status.
		for _, node := range transitioned {
			g.pushEvent(Event{
				Type:      EventHealthChange,
				Node:      node,
				Timestamp: time.Now(),
			}, allNodes)
		}
	}
}

func (g *Gossip) checkHealth(node MeshNode) bool {
	url := fmt.Sprintf("http://%s:%d/v1/health", node.Address, node.Port)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	if g.meshKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.meshKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
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
