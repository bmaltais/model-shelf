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
	url := fmt.Sprintf("http://%s/v1/jobs?id=%s", addr, jobID)
	body, err := httpGet(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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

	url := fmt.Sprintf("http://%s/v1/jobs", addr)
	if useMesh {
		url += "?mesh=true"
	}
	body, err := httpGet(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	var jobs []daemon.Job
	if err := json.Unmarshal(body, &jobs); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid response: %v\n", err)
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
	// Column-aligned output.
	// Headers: JOB ID  MODEL  TARGET  STATUS  PROGRESS  STARTED
	header := fmt.Sprintf("  %-12s  %-40s  %-12s  %-13s  %-12s  %s", "JOB ID", "MODEL", "TARGET", "STATUS", "PROGRESS", "STARTED")
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

		fmt.Printf("  %-12s  %-40s  %-12s  %-13s  %-12s  %s\n",
			shortID, model, target, j.Status, progress, started)
	}
}

func printJobDetail(j daemon.Job) {
	fmt.Printf("  job_id      %s\n", j.ID)
	fmt.Printf("  model       %s\n", j.RepoID)
	fmt.Printf("  format      %s\n", j.Format)
	if j.Quant != "" {
		fmt.Printf("  quant       %s\n", j.Quant)
	}
	fmt.Printf("  target      %s\n", j.Target)
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
		return nil, fmt.Errorf("cannot reach daemon: %v", err)
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
