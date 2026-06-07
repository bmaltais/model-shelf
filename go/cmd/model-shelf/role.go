package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func cmdRole(args []string) int {
	if len(args) < 1 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: model-shelf role <set|add|remove> <roles>")
		fmt.Println()
		fmt.Println("Manage node roles.")
		fmt.Println()
		fmt.Println("Actions:")
		fmt.Println("  set <roles>     Set roles (replaces all existing roles)")
		fmt.Println("  add <roles>     Add roles to the current set")
		fmt.Println("  remove <roles>  Remove roles from the current set")
		fmt.Println()
		fmt.Println("Roles are comma-separated: controller,store,executor")
		return 0
	}

	action := args[0]
	if action != "set" && action != "add" && action != "remove" {
		fmt.Fprintf(os.Stderr, "unknown role action: %s\n", action)
		fmt.Fprintf(os.Stderr, "usage: model-shelf role <set|add|remove> <roles>\n")
		return 1
	}

	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Printf("Usage: model-shelf role %s <roles>\n", action)
		fmt.Println()
		fmt.Println("Roles are comma-separated: controller,store,executor")
		return 0
	}

	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "error: roles argument is required\n")
		fmt.Fprintf(os.Stderr, "usage: model-shelf role %s <roles>\n", action)
		return 1
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

	// Parse and validate the provided roles.
	validRoles := map[string]bool{"controller": true, "store": true, "executor": true}
	var inputRoles []string
	for _, r := range strings.Split(args[1], ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !validRoles[r] {
			fmt.Fprintf(os.Stderr, "error: unknown role %q (valid: controller, store, executor)\n", r)
			return 1
		}
		inputRoles = append(inputRoles, r)
	}
	if len(inputRoles) == 0 {
		fmt.Fprintf(os.Stderr, "error: at least one valid role is required\n")
		return 1
	}

	// Apply the action.
	switch action {
	case "set":
		cfg.Roles = dedup(inputRoles)
	case "add":
		existing := make(map[string]bool)
		for _, r := range cfg.Roles {
			existing[r] = true
		}
		for _, r := range inputRoles {
			if !existing[r] {
				cfg.Roles = append(cfg.Roles, r)
				existing[r] = true
			}
		}
	case "remove":
		toRemove := make(map[string]bool)
		for _, r := range inputRoles {
			toRemove[r] = true
		}
		var remaining []string
		for _, r := range cfg.Roles {
			if !toRemove[r] {
				remaining = append(remaining, r)
			}
		}
		if len(remaining) == 0 {
			fmt.Fprintf(os.Stderr, "error: cannot remove all roles — node must have at least one role\n")
			return 1
		}
		cfg.Roles = remaining
	}

	// Write updated config.
	if err := meshconfig.Write(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: could not update config: %v\n", err)
		return 1
	}

	fmt.Printf("model-shelf: roles updated to [%s]\n", strings.Join(cfg.Roles, ", "))

	// Gossip the role change to peers.
	gossipRoleChange(cfg)

	return 0
}

func gossipRoleChange(cfg *meshconfig.Config) {
	nodes, err := daemon.LoadMeshState()
	if err != nil || len(nodes) == 0 {
		return
	}

	selfNode := daemon.MeshNode{
		Name:    cfg.Name,
		Address: meshconfig.GetHostname(),
		Port:    cfg.Port,
		Roles:   cfg.Roles,
		Status:  daemon.StatusOnline,
	}
	ev := daemon.Event{
		Type:      daemon.EventHealthChange,
		Node:      selfNode,
		Timestamp: time.Now(),
	}
	evData, _ := json.Marshal(ev)
	client := &http.Client{Timeout: 5 * time.Second}

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
			continue
		}
		resp.Body.Close()
	}
}

func dedup(roles []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, r := range roles {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}
