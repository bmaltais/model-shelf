// Package daemon implements the model-shelf HTTP daemon.
package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

// Daemon holds the running daemon state.
type Daemon struct {
	cfg       *meshconfig.Config
	startTime time.Time
	server    *http.Server
	gossip    *Gossip
}

// HealthResponse is returned by GET /v1/health.
type HealthResponse struct {
	Name          string   `json:"name"`
	Roles         []string `json:"roles"`
	Port          int      `json:"port"`
	DiskTotalGB   float64  `json:"disk_total_gb"`
	DiskFreeGB    float64  `json:"disk_free_gb"`
	UptimeSeconds float64  `json:"uptime_seconds"`
}

// NodeInfo describes a mesh node.
type NodeInfo struct {
	Name    string   `json:"name"`
	Address string   `json:"address"`
	Port    int      `json:"port"`
	Roles   []string `json:"roles"`
}

// JoinRequest is sent by a node wanting to join the mesh.
type JoinRequest struct {
	Name    string   `json:"name"`
	Address string   `json:"address"`
	Port    int      `json:"port"`
	Roles   []string `json:"roles"`
}

// JoinResponse is returned by POST /v1/join.
type JoinResponse struct {
	OK    bool       `json:"ok"`
	Nodes []NodeInfo `json:"nodes"`
}

// New creates a new Daemon from config.
func New(cfg *meshconfig.Config) *Daemon {
	d := &Daemon{
		cfg:       cfg,
		startTime: time.Now(),
	}
	// Register self as a node.
	selfNode := MeshNode{
		Name:    cfg.Name,
		Address: meshconfig.GetHostname(),
		Roles:   cfg.Roles,
		Port:    cfg.Port,
		Status:  StatusOnline,
	}
	d.gossip = NewGossip(selfNode, cfg.MeshKey)
	return d
}

// Run starts the HTTP server and blocks until shutdown.
func (d *Daemon) Run() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", d.handleHealth)
	mux.HandleFunc("/v1/join", d.handleJoin)
	mux.HandleFunc("/v1/nodes", d.handleNodes)
	mux.HandleFunc("/v1/events", d.handleEvents)

	handler := d.authMiddleware(mux)

	addr := fmt.Sprintf(":%d", d.cfg.Port)
	d.server = &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start gossip background poller.
	d.gossip.StartPoller(ctx)

	go func() {
		<-ctx.Done()
		log.Println("model-shelf daemon: shutting down...")
		d.gossip.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.server.Shutdown(shutdownCtx)
	}()

	log.Printf("model-shelf daemon: listening on %s (node: %s, roles: %v)", addr, d.cfg.Name, d.cfg.Roles)
	err := d.server.ListenAndServe()
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

	totalGB, freeGB := diskUsage(d.cfg.ShelfRoot)

	resp := HealthResponse{
		Name:          d.cfg.Name,
		Roles:         d.cfg.Roles,
		Port:          d.cfg.Port,
		DiskTotalGB:   totalGB,
		DiskFreeGB:    freeGB,
		UptimeSeconds: time.Since(d.startTime).Seconds(),
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
	gossipNodes := d.gossip.Nodes()
	existingNodes := make([]NodeInfo, 0, len(gossipNodes))
	for _, n := range gossipNodes {
		existingNodes = append(existingNodes, NodeInfo{
			Name:    n.Name,
			Address: n.Address,
			Port:    n.Port,
			Roles:   n.Roles,
		})
	}

	// Register in gossip state and push join event to peers.
	newNode := MeshNode{
		Name:    req.Name,
		Address: req.Address,
		Port:    req.Port,
		Roles:   req.Roles,
		Status:  StatusOnline,
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

// diskUsage returns total and free disk space in GB for the given path.
func diskUsage(path string) (totalGB, freeGB float64) {
	if path == "" {
		return 0, 0
	}
	// Check if path exists.
	if _, err := os.Stat(path); err != nil {
		return 0, 0
	}
	return diskUsagePlatform(path)
}
