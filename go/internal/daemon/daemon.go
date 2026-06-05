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
	nodes     []NodeInfo
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
	d.nodes = []NodeInfo{{
		Name:  cfg.Name,
		Roles: cfg.Roles,
		Port:  cfg.Port,
	}}
	return d
}

// Run starts the HTTP server and blocks until shutdown.
func (d *Daemon) Run() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", d.handleHealth)
	mux.HandleFunc("/v1/join", d.handleJoin)

	handler := d.authMiddleware(mux)

	addr := fmt.Sprintf(":%d", d.cfg.Port)
	d.server = &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Println("model-shelf daemon: shutting down...")
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
		http.Error(w, `{"error": "invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Port == 0 {
		http.Error(w, `{"error": "name and port are required"}`, http.StatusBadRequest)
		return
	}

	// Return existing nodes (before adding the new one) as bootstrap state.
	existingNodes := make([]NodeInfo, len(d.nodes))
	copy(existingNodes, d.nodes)

	// Register the new node.
	d.nodes = append(d.nodes, NodeInfo{
		Name:    req.Name,
		Address: req.Address,
		Port:    req.Port,
		Roles:   req.Roles,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(JoinResponse{
		OK:    true,
		Nodes: existingNodes,
	})
}

// authMiddleware checks the mesh key on all /v1/ requests.
// If no mesh key is configured, all requests are allowed.
func (d *Daemon) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
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
