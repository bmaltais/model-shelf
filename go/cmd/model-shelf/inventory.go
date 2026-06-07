package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
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
		emitError(jsonOutput, "not part of a mesh — run `model-shelf init` and `model-shelf join`")
		return 1
	}
	cfg, err := meshconfig.Load()
	if err != nil {
		emitError(jsonOutput, err.Error())
		return 1
	}

	// Get node list from local daemon.
	nodes, err := fetchNodes(cfg)
	if err != nil {
		emitError(jsonOutput, err.Error())
		return 1
	}

	// Aggregate inventory from all nodes.
	rows := aggregateInventory(nodes, cfg)
	sortInventoryRows(rows)

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
// For unreachable nodes, falls back to the local daemon's peer inventory cache and
// marks those rows as stale. Emits a warning to stderr for each unreachable node.
func aggregateInventory(nodes []daemon.MeshNode, cfg *meshconfig.Config) []InventoryRow {
	rows := []InventoryRow{}

	for _, node := range nodes {
		entries, stale := fetchNodeInventory(node, cfg)
		if stale {
			fmt.Fprintf(os.Stderr, "warning: %s unreachable — showing cached inventory\n", node.Name)
			// Fall back to the local daemon's cached inventory for this node.
			entries = fetchCachedInventory(node.Name, cfg)
		}
		for _, e := range entries {
			rows = append(rows, InventoryRow{
				Node:      node.Name,
				RepoID:    e.RepoID,
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
// Returns the entries and whether the node was unreachable (stale=true means
// the request failed; no cached data is returned).
// Offline-status nodes are queried directly — they may be reachable before the
// first gossip health poll confirms them online.
func fetchNodeInventory(node daemon.MeshNode, cfg *meshconfig.Config) ([]daemon.InventoryEntry, bool) {
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

// fetchCachedInventory queries the local daemon for a peer node's cached inventory.
// Returns an empty slice if no cache exists or the daemon is unreachable.
func fetchCachedInventory(nodeName string, cfg *meshconfig.Config) []daemon.InventoryEntry {
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/peer-inventory?node=%s", cfg.Port, nodeName)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	if cfg.MeshKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.MeshKey)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var entries []daemon.InventoryEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil
	}
	return entries
}

func formatOrder(format string) int {
	switch format {
	case "gguf":
		return 0
	case "mlx":
		return 1
	case "safetensors":
		return 2
	default:
		return 3
	}
}

func sortInventoryRows(rows []InventoryRow) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Node != b.Node {
			return a.Node < b.Node
		}
		fa, fb := formatOrder(a.Format), formatOrder(b.Format)
		if fa != fb {
			return fa < fb
		}
		return a.RepoID < b.RepoID
	})
}

func printInventoryTable(rows []InventoryRow) {
	if len(rows) == 0 {
		fmt.Println("No models reported by online nodes.")
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
		// Build combined model display string (repo_id:quant) for table output.
		model := r.RepoID
		if r.Quant != "" {
			model += ":" + r.Quant
		}
		tRows[i] = tableRow{
			node:   nodeName,
			model:  model,
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

	// Print header (truncate headers to column width for narrow terminals).
	fmtStr := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%-%ds\n",
		widths[0], widths[1], widths[2], widths[3])
	fmt.Printf(fmtStr,
		truncate(headers[0], widths[0]),
		truncate(headers[1], widths[1]),
		truncate(headers[2], widths[2]),
		truncate(headers[3], widths[3]))

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


