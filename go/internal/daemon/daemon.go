// Package daemon implements the model-shelf HTTP daemon.
package daemon

import (
	"context"
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

// New creates a new Daemon from config.
func New(cfg *meshconfig.Config) *Daemon {
	return &Daemon{
		cfg:       cfg,
		startTime: time.Now(),
	}
}

// Run starts the HTTP server and blocks until shutdown.
func (d *Daemon) Run() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", d.handleHealth)

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

	log.Printf("model-shelf daemon: listening on %s (node: %s, roles: %v)\n", addr, d.cfg.Name, d.cfg.Roles)
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
		if auth != expected {
			http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
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
