package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func cmdStatus(args []string) int {
	positional, flags := parseFlags(args)

	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf status [<job_id>] [--json] [--mesh] [--local]")
		fmt.Println()
		fmt.Println("Show job status for all in-flight and recent jobs.")
		fmt.Println()
		fmt.Println("By default, aggregates jobs from all nodes when this node is a controller")
		fmt.Println("or has seeds configured. Use --local to show only local jobs.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --json             Emit JSON output")
		fmt.Println("  --mesh             Aggregate jobs from all nodes in the mesh")
		fmt.Println("  --local            Show only local daemon jobs (override mesh default)")
		return 0
	}

	// Determine daemon address.
	addr := daemonAddr()

	// Single job detail.
	if len(positional) > 0 {
		jobID := positional[0]
		return statusSingleJob(addr, jobID, flags)
	}

	// All jobs.
	return statusAllJobs(addr, flags)
}

func statusSingleJob(addr, jobID string, flags map[string]string) int {
	jsonOutput := flags["json"] == "true"
	url := fmt.Sprintf("http://%s/v1/jobs?id=%s", addr, jobID)
	body, err := httpGet(url)
	if err != nil {
		emitError(jsonOutput, err.Error())
		return 1
	}

	var job daemon.Job
	if err := json.Unmarshal(body, &job); err != nil {
		// Check if it's an error response.
		var errResp map[string]string
		if json.Unmarshal(body, &errResp) == nil && errResp["error"] != "" {
			fmt.Fprintf(os.Stderr, "error: %s\n", errResp["error"])
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: invalid response: %v\n", err)
		return 1
	}

	if flags["json"] == "true" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(job)
		return 0
	}

	printJobDetail(job)
	return 0
}

// shouldDefaultMesh returns true if the node is a controller or has seeds
// configured, meaning status should aggregate from all mesh nodes by default.
func shouldDefaultMesh() bool {
	if !meshconfig.Exists() {
		return false
	}
	cfg, err := meshconfig.Load()
	if err != nil {
		return false
	}
	if len(cfg.Seeds) > 0 {
		return true
	}
	for _, r := range cfg.Roles {
		if r == "controller" {
			return true
		}
	}
	return false
}

// findControllerAddr queries the local daemon's /v1/nodes endpoint and returns
// the address:port of the first online controller node. Returns "" if none is
// found or on any error.
func findControllerAddr(daemonAddr string) string {
	nodesURL := fmt.Sprintf("http://%s/v1/nodes", daemonAddr)
	body, err := httpGet(nodesURL)
	if err != nil {
		return ""
	}
	var nodes []daemon.MeshNode
	if err := json.Unmarshal(body, &nodes); err != nil {
		return ""
	}
	for _, n := range nodes {
		if n.Status != daemon.StatusOnline {
			continue
		}
		if daemon.HasRole(n.Roles, "controller") {
			return fmt.Sprintf("%s:%d", n.Address, n.Port)
		}
	}
	return ""
}

func statusAllJobs(addr string, flags map[string]string) int {
	// Determine whether to query mesh-wide.
	// Explicit --local forces local-only; explicit --mesh forces mesh-wide.
	// Otherwise, default to mesh-wide if this node is a controller or has seeds.
	if flags["local"] == "true" && flags["mesh"] == "true" {
		fmt.Fprintf(os.Stderr, "error: --local and --mesh are mutually exclusive\n")
		return 1
	}

	useMesh := false
	if flags["local"] == "true" {
		useMesh = false
	} else if flags["mesh"] == "true" {
		useMesh = true
	} else {
		useMesh = shouldDefaultMesh()
	}

	// Non-controller nodes have an incomplete view of the mesh (they only see
	// jobs that gossip has replicated to them). When mesh-wide status is
	// requested, proxy to the controller so the caller always sees a complete
	// picture, matching what the controller itself would return.
	if useMesh {
		localCfg, cfgErr := meshconfig.Load()
		if cfgErr == nil && !isControllerNode(localCfg) {
			if controllerAddr := findControllerAddr(addr); controllerAddr != "" {
				addr = controllerAddr
			}
		}
	}

	jsonOutput := flags["json"] == "true"
	url := fmt.Sprintf("http://%s/v1/jobs", addr)
	if useMesh {
		url += "?mesh=true"
	}
	body, err := httpGet(url)
	if err != nil {
		emitError(jsonOutput, err.Error())
		return 1
	}

	var jobs []daemon.Job
	if err := json.Unmarshal(body, &jobs); err != nil {
		emitError(jsonOutput, "invalid response from daemon")
		return 1
	}

	if flags["json"] == "true" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(jobs)
		return 0
	}

	if len(jobs) == 0 {
		fmt.Println("(no jobs)")
		return 0
	}

	// Sort: active jobs first (by created_at desc), then completed/failed (by done_at desc).
	sort.Slice(jobs, func(i, j int) bool {
		iActive := jobs[i].Status == daemon.JobQueued || jobs[i].Status == daemon.JobDownloading || jobs[i].Status == daemon.JobTransferring || jobs[i].Status == daemon.JobEvicting
		jActive := jobs[j].Status == daemon.JobQueued || jobs[j].Status == daemon.JobDownloading || jobs[j].Status == daemon.JobTransferring || jobs[j].Status == daemon.JobEvicting
		if iActive != jActive {
			return iActive
		}
		// For completed/failed jobs, sort by done_at descending.
		if !iActive && jobs[i].DoneAt != nil && jobs[j].DoneAt != nil {
			return jobs[i].DoneAt.After(*jobs[j].DoneAt)
		}
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})

	printJobTable(jobs)
	return 0
}

func printJobTable(jobs []daemon.Job) {
	// Determine whether any transfer jobs are present (to decide whether to show SOURCE column).
	hasTransfer := false
	for _, j := range jobs {
		if j.Type == daemon.JobTypeTransfer {
			hasTransfer = true
			break
		}
	}

	// Column-aligned output.
	var header string
	if hasTransfer {
		// Headers: JOB ID  MODEL  TARGET  SOURCE  STATUS  PROGRESS  STARTED
		header = fmt.Sprintf("  %-12s  %-40s  %-12s  %-12s  %-13s  %-12s  %s", "JOB ID", "MODEL", "TARGET", "SOURCE", "STATUS", "PROGRESS", "STARTED")
	} else {
		// Headers: JOB ID  MODEL  TARGET  STATUS  PROGRESS  STARTED
		header = fmt.Sprintf("  %-12s  %-40s  %-12s  %-13s  %-12s  %s", "JOB ID", "MODEL", "TARGET", "STATUS", "PROGRESS", "STARTED")
	}
	fmt.Println(header)
	fmt.Println("  " + strings.Repeat("-", len(header)-2))

	for _, j := range jobs {
		shortID := j.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		model := j.RepoID
		if len(model) > 40 {
			model = model[:37] + "..."
		}
		target := j.Target
		if len(target) > 12 {
			target = target[:12]
		}

		progress := progressStr(j)
		started := relativeTime(j.CreatedAt)

		if hasTransfer {
			source := j.Source
			if len(source) > 12 {
				source = source[:12]
			}
			fmt.Printf("  %-12s  %-40s  %-12s  %-12s  %-13s  %-12s  %s\n",
				shortID, model, target, source, j.Status, progress, started)
		} else {
			fmt.Printf("  %-12s  %-40s  %-12s  %-13s  %-12s  %s\n",
				shortID, model, target, j.Status, progress, started)
		}
	}
}

func printJobDetail(j daemon.Job) {
	fmt.Printf("  job_id      %s\n", j.ID)
	fmt.Printf("  type        %s\n", j.Type)
	fmt.Printf("  model       %s\n", j.RepoID)
	fmt.Printf("  format      %s\n", j.Format)
	if j.Quant != "" {
		fmt.Printf("  quant       %s\n", j.Quant)
	}
	fmt.Printf("  target      %s\n", j.Target)
	if j.Source != "" {
		fmt.Printf("  source      %s\n", j.Source)
	}
	fmt.Printf("  status      %s\n", j.Status)
	fmt.Printf("  progress    %s\n", progressStr(j))
	fmt.Printf("  started     %s\n", j.CreatedAt.Format(time.RFC3339))
	if j.DoneAt != nil {
		fmt.Printf("  finished    %s\n", j.DoneAt.Format(time.RFC3339))
		fmt.Printf("  duration    %s\n", j.DoneAt.Sub(j.CreatedAt).Round(time.Second))
	}
	if j.Error != "" {
		fmt.Printf("  error       %s\n", j.Error)
	}
}

func progressStr(j daemon.Job) string {
	if j.Status == daemon.JobAlreadyPresent {
		return "skipped"
	}
	if j.BytesTotal <= 0 {
		switch j.Status {
		case daemon.JobCompleted:
			return "done"
		case daemon.JobFailed:
			return "-"
		default:
			return "..."
		}
	}
	pct := float64(j.BytesDownloaded) / float64(j.BytesTotal) * 100
	return fmt.Sprintf("%s/%s (%.0f%%)", fmtBytes(j.BytesDownloaded), fmtBytes(j.BytesTotal), pct)
}

func fmtBytes(n int64) string {
	size := float64(n)
	for _, unit := range []string{"B", "KB", "MB", "GB", "TB"} {
		if size < 1024 || unit == "TB" {
			if unit == "B" {
				return fmt.Sprintf("%.0f%s", size, unit)
			}
			return fmt.Sprintf("%.1f%s", size, unit)
		}
		size /= 1024
	}
	return fmt.Sprintf("%.1fTB", size)
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func daemonAddr() string {
	// Try to load port from mesh config; fall back to default.
	port := meshconfig.DefaultPort
	if meshconfig.Exists() {
		if cfg, err := meshconfig.Load(); err == nil {
			port = cfg.Port
		}
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func httpGet(url string) ([]byte, error) {
	meshKey := loadMeshKey()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if meshKey != "" {
		req.Header.Set("Authorization", "Bearer "+meshKey)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s", errDaemonNotRunning)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		if json.Unmarshal(body, &errResp) == nil && errResp["error"] != "" {
			return nil, fmt.Errorf("%s", errResp["error"])
		}
		return nil, fmt.Errorf("daemon returned HTTP %d", resp.StatusCode)
	}
	return body, nil
}
