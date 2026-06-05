package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func cmdNodes(args []string) int {
	_, flags := parseFlags(args)
	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf nodes [--json]")
		fmt.Println()
		fmt.Println("List mesh nodes.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --json             Emit JSON output")
		return 0
	}
	jsonOutput := flags["json"] == "true"

	// Load config to determine daemon port and mesh key.
	if !meshconfig.Exists() {
		fmt.Fprintf(os.Stderr, "error: mesh not configured. Run 'model-shelf init' first.\n")
		return 1
	}
	cfg, err := meshconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Query local daemon.
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/nodes", cfg.Port)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if cfg.MeshKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.MeshKey)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon not running — start with `model-shelf service start` (%v)\n", err)
		return 1
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading response: %v\n", err)
		return 1
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "error: daemon returned %d: %s\n", resp.StatusCode, string(body))
		return 1
	}

	var nodes []daemon.MeshNode
	if err := json.Unmarshal(body, &nodes); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid response from daemon: %v\n", err)
		return 1
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(nodes)
		return 0
	}

	printNodesTable(nodes)
	return 0
}

func printNodesTable(nodes []daemon.MeshNode) {
	if len(nodes) == 0 {
		fmt.Println("No nodes in mesh.")
		return
	}

	// Determine terminal width (default 80 if not a terminal).
	termWidth := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		termWidth = w
	}

	// Compute column widths from content.
	headers := []string{"NAME", "ROLES", "STATUS", "DISK FREE", "LAST SEEN"}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}

	type row struct {
		name     string
		roles    string
		status   string
		diskFree string
		lastSeen string
	}
	rows := make([]row, len(nodes))
	for i, n := range nodes {
		roles := strings.Join(n.Roles, ",")
		status := string(n.Status)

		// Format disk free from gossip state.
		diskFree := "-"
		if n.DiskFreeGB > 0 {
			diskFree = fmt.Sprintf("%.1f GB", n.DiskFreeGB)
		}
		lastSeen := "-"
		if n.LastSeen != nil {
			elapsed := time.Since(*n.LastSeen)
			lastSeen = formatDuration(elapsed)
		}

		rows[i] = row{n.Name, roles, status, diskFree, lastSeen}

		if len(n.Name) > widths[0] {
			widths[0] = len(n.Name)
		}
		if len(roles) > widths[1] {
			widths[1] = len(roles)
		}
		if len(status) > widths[2] {
			widths[2] = len(status)
		}
		if len(diskFree) > widths[3] {
			widths[3] = len(diskFree)
		}
		if len(lastSeen) > widths[4] {
			widths[4] = len(lastSeen)
		}
	}

	// Truncate columns to fit terminal width.
	// Separators take 2 chars each (4 gaps × 2 = 8 chars).
	const gapWidth = 2
	totalGaps := (len(headers) - 1) * gapWidth
	totalContent := 0
	for _, w := range widths {
		totalContent += w
	}
	// If content + gaps exceed terminal, shrink the widest columns.
	for totalContent+totalGaps > termWidth {
		// Find the widest column and shrink it by 1.
		maxIdx := 0
		for i := 1; i < len(widths); i++ {
			if widths[i] > widths[maxIdx] {
				maxIdx = i
			}
		}
		if widths[maxIdx] <= 3 {
			break // Don't shrink below 3 chars.
		}
		widths[maxIdx]--
		totalContent--
	}

	// Print header.
	fmtStr := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%-%ds  %%-%ds\n",
		widths[0], widths[1], widths[2], widths[3], widths[4])
	fmt.Printf(fmtStr, headers[0], headers[1], headers[2], headers[3], headers[4])

	// Print rows (truncate values to column width).
	for _, r := range rows {
		fmt.Printf(fmtStr,
			truncate(r.name, widths[0]),
			truncate(r.roles, widths[1]),
			truncate(r.status, widths[2]),
			truncate(r.diskFree, widths[3]),
			truncate(r.lastSeen, widths[4]),
		)
	}
}

// formatDuration returns a human-friendly "ago" string.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return "just now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// truncate shortens s to maxLen, adding "…" if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}
