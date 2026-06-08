// Package daemon implements the model-shelf HTTP daemon.
package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
	"github.com/alexziskind1/model-shelf/internal/resolver"
)

// Daemon holds the running daemon state.
type Daemon struct {
	cfg       *meshconfig.Config
	startTime time.Time
	server    *http.Server
	gossip    *Gossip
	inventory *Inventory
	jobs      *JobStore
	gpu       *GPUInfo
	evictMu   sync.Mutex // serializes eviction cascades to prevent double-counting freed space
}

// HealthResponse is returned by GET /v1/health.
type HealthResponse struct {
	Name          string   `json:"name"`
	Roles         []string `json:"roles"`
	Port          int      `json:"port"`
	DiskTotalGB   float64  `json:"disk_total_gb"`
	DiskFreeGB    float64  `json:"disk_free_gb"`
	UptimeSeconds float64  `json:"uptime_seconds"`
	GPU           *GPUInfo `json:"gpu"`
	Version       string   `json:"version"`
}

// NodeInfo describes a mesh node.
type NodeInfo struct {
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Port        int      `json:"port"`
	Roles       []string `json:"roles"`
	DiskFreeGB  float64  `json:"disk_free_gb,omitempty"`
	DiskTotalGB float64  `json:"disk_total_gb,omitempty"`
	GPU         *GPUInfo `json:"gpu"`
}

// JoinRequest is sent by a node wanting to join the mesh.
type JoinRequest struct {
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Port        int      `json:"port"`
	Roles       []string `json:"roles"`
	DiskFreeGB  float64  `json:"disk_free_gb,omitempty"`
	DiskTotalGB float64  `json:"disk_total_gb,omitempty"`
	GPU         *GPUInfo `json:"gpu"`
}

// JoinResponse is returned by POST /v1/join.
type JoinResponse struct {
	OK    bool       `json:"ok"`
	Nodes []NodeInfo `json:"nodes"`
}

// New creates a new Daemon from config.
func New(cfg *meshconfig.Config) *Daemon {
	// Detect GPU hardware (or use manual override from config).
	gpuInfo := DetectGPU(cfg.GPU)

	d := &Daemon{
		cfg:       cfg,
		startTime: time.Now(),
		gpu:       gpuInfo,
	}
	// Register self as a node.
	totalGB, freeGB := DiskUsage(cfg.ShelfRoot)
	now := time.Now()
	selfNode := MeshNode{
		Name:        cfg.Name,
		Address:     meshconfig.GetHostname(),
		Roles:       cfg.Roles,
		Port:        cfg.Port,
		Status:      StatusOnline,
		DiskFreeGB:  freeGB,
		DiskTotalGB: totalGB,
		GPU:         gpuInfo,
		LastSeen:    &now,
		Version:     cfg.Version,
	}
	// Load or create inventory and scan shelf.
	inv, err := LoadInventory()
	if err != nil {
		log.Printf("model-shelf daemon: failed to load inventory: %v (starting fresh)", err)
		inv = NewInventory()
	}
	if cfg.ShelfRoot != "" {
		if err := inv.ScanShelf(cfg.ShelfRoot); err != nil {
			log.Printf("model-shelf daemon: inventory scan error: %v", err)
		}
		if err := inv.Save(); err != nil {
			log.Printf("model-shelf daemon: failed to persist inventory: %v", err)
		}
	}
	d.inventory = inv
	d.jobs = NewJobStore()
	d.gossip = NewGossip(selfNode, cfg.MeshKey, cfg.ShelfRoot, d.startTime, d.jobs)
	d.gossip.SetGPUConfig(cfg.GPU)

	// Bootstrap from seeds to ensure non-controller nodes discover all peers.
	// This contacts the seed node to fetch the full mesh state.
	d.gossip.BootstrapFromSeeds(cfg.Seeds)

	return d
}

// Run starts the HTTP server and blocks until shutdown.
func (d *Daemon) Run() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", d.handleHealth)
	mux.HandleFunc("/v1/join", d.handleJoin)
	mux.HandleFunc("/v1/nodes", d.handleNodes)
	mux.HandleFunc("/v1/events", d.handleEvents)
	mux.HandleFunc("/v1/inventory", d.handleInventory)
	mux.HandleFunc("/v1/inventory/rescan", d.handleInventoryRescan)
	mux.HandleFunc("/v1/peer-inventory", d.handlePeerInventory)
	mux.HandleFunc("/v1/pull", d.handlePull)
	mux.HandleFunc("/v1/jobs", d.handleJobs)
	mux.HandleFunc("/v1/status", d.handleJobs)
	mux.HandleFunc("/v1/models/", d.handleModelDownload)
	mux.HandleFunc("/v1/upgrade", d.handleUpgrade)

	handler := d.authMiddleware(mux)

	addr := fmt.Sprintf(":%d", d.cfg.Port)
	d.server = &http.Server{
		Handler: handler,
		// Raise OS-level TCP send buffer (SO_SNDBUF) on new connections so the
		// server can keep the pipe full when a peer receiver has a large window.
		// Only the send buffer matters here: this node only sends model files;
		// incoming requests are small. SO_RCVBUF is enlarged on the client side
		// in transferFromPeer's DialContext instead.
		ConnState: func(conn net.Conn, state http.ConnState) {
			if state == http.StateNew {
				if tc, ok := conn.(*net.TCPConn); ok {
					_ = tc.SetWriteBuffer(transferSocketBufSize)
				}
			}
		},
	}

	// Bind the port before logging — ensures "listening" only prints on success.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start gossip background poller.
	d.gossip.StartPoller(ctx)

	// Start periodic inventory shelf scan (picks up models added externally).
	d.startInventoryScanner(ctx)

	// Start watchdog to detect and fail stalled jobs.
	d.startWatchdog(ctx.Done())

	go func() {
		<-ctx.Done()
		log.Println("model-shelf daemon: shutting down...")
		d.gossip.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.server.Shutdown(shutdownCtx)
	}()

	log.Printf("model-shelf daemon: listening on %s (node: %s, roles: %v)", addr, d.cfg.Name, d.cfg.Roles)
	err = d.server.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	totalGB, freeGB := DiskUsage(d.cfg.ShelfRoot)

	// Refresh GPU available VRAM (local copy to avoid data race on d.gpu).
	gpuInfo := RefreshGPUAvailableVRAM(d.gpu, d.cfg.GPU)

	resp := HealthResponse{
		Name:          d.cfg.Name,
		Roles:         d.cfg.Roles,
		Port:          d.cfg.Port,
		DiskTotalGB:   totalGB,
		DiskFreeGB:    freeGB,
		UptimeSeconds: time.Since(d.startTime).Seconds(),
		GPU:           gpuInfo,
		Version:       d.cfg.Version,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (d *Daemon) handleJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req JoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid request body"}`))
		return
	}
	if req.Name == "" || req.Port == 0 || req.Address == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "name, address, and port are required"}`))
		return
	}

	// Get existing nodes from gossip state (before adding the new one).
	// Filter out the joining node itself — it doesn't need to bootstrap itself.
	gossipNodes := d.gossip.Nodes()
	existingNodes := make([]NodeInfo, 0, len(gossipNodes))
	for _, n := range gossipNodes {
		if n.Name == req.Name {
			continue
		}
		existingNodes = append(existingNodes, NodeInfo{
			Name:        n.Name,
			Address:     n.Address,
			Port:        n.Port,
			Roles:       n.Roles,
			DiskFreeGB:  n.DiskFreeGB,
			DiskTotalGB: n.DiskTotalGB,
			GPU:         n.GPU,
		})
	}

	// Register in gossip state and push join event to peers.
	now := time.Now()
	newNode := MeshNode{
		Name:        req.Name,
		Address:     req.Address,
		Port:        req.Port,
		Roles:       req.Roles,
		Status:      StatusOnline,
		DiskFreeGB:  req.DiskFreeGB,
		DiskTotalGB: req.DiskTotalGB,
		GPU:         req.GPU,
		LastSeen:    &now,
	}
	d.gossip.AddNode(newNode)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(JoinResponse{
		OK:    true,
		Nodes: existingNodes,
	})
}

func (d *Daemon) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nodes := d.gossip.Nodes()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nodes)
}

func (d *Daemon) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var ev Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid event body"}`))
		return
	}

	// Validate event type and required fields.
	if !validEventType(ev.Type) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "unknown event type"}`))
		return
	}
	if ev.Node.Name == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "event node name is required"}`))
		return
	}

	d.gossip.ApplyEvent(ev)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok": true}`))
}

func (d *Daemon) handleInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries := d.inventory.Entries()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

// handleInventoryRescan triggers an immediate shelf rescan and returns the
// updated inventory. Used by the CLI after downloading a model to notify the
// daemon without waiting for the periodic scan.
func (d *Daemon) handleInventoryRescan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := &resolver.Config{ShelfRoot: d.cfg.ShelfRoot}
	for _, root := range resolver.ListShelfCandidates(cfg) {
		if err := d.inventory.ScanShelf(root); err != nil {
			log.Printf("model-shelf daemon: rescan error (%s): %v", root, err)
		}
	}
	if err := d.inventory.Save(); err != nil {
		log.Printf("model-shelf daemon: failed to persist inventory after rescan: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handlePeerInventory returns the cached inventory for a named peer node.
// Used by the CLI to surface stale entries when a node is offline.
// GET /v1/peer-inventory?node=<name>
func (d *Daemon) handlePeerInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nodeName := r.URL.Query().Get("node")
	if nodeName == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "node query parameter required"}`))
		return
	}
	entries := d.gossip.PeerInventory(nodeName)
	if entries == nil {
		entries = []InventoryEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

// GetInventory returns the daemon's inventory (used by resolve to touch models).
func (d *Daemon) GetInventory() *Inventory {
	return d.inventory
}

// authMiddleware checks the mesh key on all /v1/ requests.
// If no mesh key is configured, all requests are allowed.
func (d *Daemon) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}

		// Health endpoint is always public (liveness probe).
		if r.URL.Path == "/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		meshKey := d.cfg.MeshKey
		if meshKey == "" {
			// No key configured — allow all (standalone mode).
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		expected := "Bearer " + meshKey
		if subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// DiskUsage returns total and free disk space in GB for the given path.
// Exported so the CLI can include disk metrics in join requests.
func DiskUsage(path string) (totalGB, freeGB float64) {
	if path == "" {
		return 0, 0
	}
	// Check if path exists.
	if _, err := os.Stat(path); err != nil {
		return 0, 0
	}
	return diskUsagePlatform(path)
}

// looksLikeModelDir checks if a path is a directory containing config.json.
func looksLikeModelDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(path, "config.json"))
	return err == nil
}

// inventoryScanInterval controls how often the daemon re-scans the shelf
// for models added externally (via resolve, manual placement, etc.).
const inventoryScanInterval = 30 * time.Second

// startInventoryScanner launches a background goroutine that periodically
// re-scans all shelf candidates and reconciles inventory.
func (d *Daemon) startInventoryScanner(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(inventoryScanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cfg := &resolver.Config{ShelfRoot: d.cfg.ShelfRoot}
				for _, root := range resolver.ListShelfCandidates(cfg) {
					if err := d.inventory.ScanShelf(root); err != nil {
						log.Printf("model-shelf daemon: inventory rescan error (%s): %v", root, err)
					}
				}
				if err := d.inventory.Save(); err != nil {
					log.Printf("model-shelf daemon: failed to persist inventory after rescan: %v", err)
				}
			}
		}
	}()
}
