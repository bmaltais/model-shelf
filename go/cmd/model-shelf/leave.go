package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func cmdLeave(args []string) int {
	_, flags := parseFlags(args)
	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf leave")
		fmt.Println()
		fmt.Println("Leave the mesh. Gossips departure to all peers, then clears local")
		fmt.Println("mesh state (seeds, mesh.json, mesh.key). Config and models remain.")
		fmt.Println("The daemon will be restarted to enter standalone mode (no mesh).")
		return 0
	}

	if !meshconfig.Exists() {
		fmt.Fprintf(os.Stderr, "error: not part of a mesh — run `model-shelf init` and `model-shelf join`\n")
		return 1
	}

	cfg, err := meshconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Build a leave event to gossip to peers.
	selfNode := daemon.MeshNode{
		Name:    cfg.Name,
		Address: meshconfig.GetHostname(),
		Port:    cfg.Port,
		Roles:   cfg.Roles,
		Status:  daemon.StatusOffline,
	}
	ev := daemon.Event{
		Type:      daemon.EventLeave,
		Node:      selfNode,
		Timestamp: time.Now(),
	}

	// Load mesh state to find peers.
	nodes, err := daemon.LoadMeshState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load mesh state: %v\n", err)
	}

	// Push departure event to all peers.
	evData, _ := json.Marshal(ev)
	client := &http.Client{Timeout: 5 * time.Second}
	peersNotified := 0
	for _, n := range nodes {
		if n.Name == cfg.Name {
			continue
		}
		url := fmt.Sprintf("http://%s:%d/v1/events", n.Address, n.Port)
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(evData))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if cfg.MeshKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.MeshKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not notify %s: %v\n", n.Name, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			peersNotified++
		}
	}

	// Clear local mesh state: mesh.json, mesh.key, and seeds from config.
	// Remove mesh.json.
	meshStatePath := meshconfig.MeshStatePath()
	if err := os.Remove(meshStatePath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", meshStatePath, err)
	}

	// Remove mesh.key.
	meshKeyPath := meshconfig.MeshKeyPath()
	if err := os.Remove(meshKeyPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", meshKeyPath, err)
	}

	// Clear seeds from config but keep the rest.
	cfg.Seeds = nil
	cfg.MeshKey = ""
	if err := meshconfig.Write(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: could not update config: %v\n", err)
		return 1
	}

	if peersNotified > 0 {
		fmt.Printf("model-shelf: left mesh (notified %d peer(s))\n", peersNotified)
	} else if len(nodes) > 1 {
		fmt.Printf("model-shelf: left mesh (could not reach any peers)\n")
	} else {
		fmt.Printf("model-shelf: left mesh\n")
	}
	fmt.Printf("model-shelf: cleared mesh key and state\n")

	// Restart the daemon so it picks up the cleared key and enters standalone mode.
	if !restartDaemonIfRunning("restarted daemon in standalone mode") {
		fmt.Printf("model-shelf: daemon continues in standalone mode\n")
	}

	return 0
}
