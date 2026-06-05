package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

// InventoryRow is a single row in the inventory table output.
type InventoryRow struct {
	Node      string `json:"node"`
	RepoID    string `json:"repo_id"`
	Quant     string `json:"quant,omitempty"`
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
	Stale     bool   `json:"stale,omitempty"`
}

func cmdInventory(args []string) int {
	_, flags := parseFlags(args)
	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf inventory [--json]")
		fmt.Println()
		fmt.Println("List models across all mesh nodes.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --json             Emit JSON output")
		return 0
	}
	jsonOutput := flags["json"] == "true"

	// Load config to determine daemon port and mesh key.
	if !meshconfig.Exists() {
		fmt.Fprintf(os.Stderr, "error: not part of a mesh — run `model-shelf init` and `model-shelf join`\n")
		return 1
	}
	cfg, err := meshconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Get node list from local daemon.
	nodes, err := fetchNodes(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	// Aggregate inventory from all nodes.
	rows := aggregateInventory(nodes, cfg)

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rows)
		return 0
	}

	printInventoryTable(rows)
	return 0
}

// fetchNodes queries the local daemon for the list of mesh nodes.
func fetchNodes(cfg *meshconfig.Config) ([]daemon.MeshNode, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/nodes", cfg.Port)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("error: %v", err)
	}
	if cfg.MeshKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.MeshKey)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon not running — start with `model-shelf service start` (%v)", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error: reading response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error: daemon returned %d: %s", resp.StatusCode, string(body))
	}

	var nodes []daemon.MeshNode
	if err := json.Unmarshal(body, &nodes); err != nil {
		return nil, fmt.Errorf("error: invalid response from daemon: %v", err)
	}
	return nodes, nil
}

// aggregateInventory queries each node's /v1/inventory and builds a combined list.
func aggregateInventory(nodes []daemon.MeshNode, cfg *meshconfig.Config) []InventoryRow {
	var rows []InventoryRow

	for _, node := range nodes {
		entries, stale := fetchNodeInventory(node, cfg)
		for _, e := range entries {
			model := e.RepoID
			if e.Quant != "" {
				model += ":" + e.Quant
			}
			rows = append(rows, InventoryRow{
				Node:      node.Name,
				RepoID:    model,
				Quant:     e.Quant,
				Format:    e.Format,
				SizeBytes: e.SizeBytes,
				Stale:     stale,
			})
		}
	}
	return rows
}

// fetchNodeInventory queries a single node's /v1/inventory endpoint.
// Returns the entries and whether the data is stale (node offline, data from cache).
func fetchNodeInventory(node daemon.MeshNode, cfg *meshconfig.Config) ([]daemon.InventoryEntry, bool) {
	if node.Status == daemon.StatusOffline {
		// Node offline — skip (no cached inventory per-node available via API).
		return nil, true
	}

	url := fmt.Sprintf("http://%s:%d/v1/inventory", node.Address, node.Port)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, true
	}
	if cfg.MeshKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.MeshKey)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, true
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, true
	}

	var entries []daemon.InventoryEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, true
	}
	return entries, false
}

func printInventoryTable(rows []InventoryRow) {
	if len(rows) == 0 {
		fmt.Println("No models in mesh.")
		return
	}

	// Determine terminal width.
	termWidth := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		termWidth = w
	}

	// Column headers.
	headers := []string{"NODE", "MODEL", "FORMAT", "SIZE"}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}

	type tableRow struct {
		node   string
		model  string
		format string
		size   string
	}

	tRows := make([]tableRow, len(rows))
	for i, r := range rows {
		nodeName := r.Node
		if r.Stale {
			nodeName += " (stale)"
		}
		tRows[i] = tableRow{
			node:   nodeName,
			model:  r.RepoID,
			format: r.Format,
			size:   fmtSize(r.SizeBytes),
		}
		if len(tRows[i].node) > widths[0] {
			widths[0] = len(tRows[i].node)
		}
		if len(tRows[i].model) > widths[1] {
			widths[1] = len(tRows[i].model)
		}
		if len(tRows[i].format) > widths[2] {
			widths[2] = len(tRows[i].format)
		}
		if len(tRows[i].size) > widths[3] {
			widths[3] = len(tRows[i].size)
		}
	}

	// Truncate columns to fit terminal width.
	const gapWidth = 2
	totalGaps := (len(headers) - 1) * gapWidth
	totalContent := 0
	for _, w := range widths {
		totalContent += w
	}
	for totalContent+totalGaps > termWidth {
		maxIdx := 0
		for i := 1; i < len(widths); i++ {
			if widths[i] > widths[maxIdx] {
				maxIdx = i
			}
		}
		if widths[maxIdx] <= 3 {
			break
		}
		widths[maxIdx]--
		totalContent--
	}

	// Print header.
	fmtStr := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%-%ds\n",
		widths[0], widths[1], widths[2], widths[3])
	fmt.Printf(fmtStr, headers[0], headers[1], headers[2], headers[3])

	// Print rows grouped by node.
	for _, r := range tRows {
		fmt.Printf(fmtStr,
			truncate(r.node, widths[0]),
			truncate(r.model, widths[1]),
			truncate(r.format, widths[2]),
			truncate(r.size, widths[3]),
		)
	}
}


