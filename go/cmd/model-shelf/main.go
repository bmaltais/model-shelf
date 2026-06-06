// Command model-shelf is a local-first resolver for Hugging Face models.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexziskind1/model-shelf/internal/config"
	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
	"github.com/alexziskind1/model-shelf/internal/resolver"
	"github.com/alexziskind1/model-shelf/internal/search"
	"github.com/alexziskind1/model-shelf/internal/service"
)

const version = "0.14.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "resolve":
		os.Exit(cmdResolve(os.Args[2:]))
	case "init":
		os.Exit(cmdInit(os.Args[2:]))
	case "find":
		os.Exit(cmdFind(os.Args[2:]))
	case "list":
		os.Exit(cmdList(os.Args[2:]))
	case "daemon":
		os.Exit(cmdDaemon(os.Args[2:]))
	case "join":
		os.Exit(cmdJoin(os.Args[2:]))
	case "leave":
		os.Exit(cmdLeave(os.Args[2:]))
	case "nodes":
		os.Exit(cmdNodes(os.Args[2:]))
	case "inventory":
		os.Exit(cmdInventory(os.Args[2:]))
	case "pull":
		os.Exit(cmdPull(os.Args[2:]))
	case "status":
		os.Exit(cmdStatus(os.Args[2:]))
	case "role":
		os.Exit(cmdRole(os.Args[2:]))
	case "service":
		os.Exit(cmdService(os.Args[2:]))
	case "version", "--version", "-v":
		fmt.Printf("model-shelf %s (go)\n", version)
		os.Exit(0)
	case "help", "--help", "-h":
		printUsage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `model-shelf %s — local-first Hugging Face model resolver

Usage:
  model-shelf init --role <roles> --shelf <path>  Initialize mesh node
  model-shelf join <peer> [--key <mesh-key>]      Join an existing mesh
  model-shelf leave                    Leave the mesh
  model-shelf resolve <repo_id>        Resolve a model to a local path
  model-shelf find <query>             Search Hugging Face for models
  model-shelf list                     List shelf contents
  model-shelf nodes [--json]            List mesh nodes
  model-shelf inventory [--json]        List models across all mesh nodes
  model-shelf pull <repo> --target <node> Pull a model to a target node
  model-shelf status [<job_id>] [--json] Show job status
  model-shelf role <set|add|remove>    Manage node roles
  model-shelf daemon                   Start the mesh daemon (foreground)
  model-shelf service <action>         Manage the system service
  model-shelf version                  Print version

Join flags:
  --key <key>        Mesh key (prompts if not provided)

Pull flags:
  --target <node>    Target node name (required)
  --quant <Q>        Quantization level (required for GGUF)
  --format <F>       Force format: gguf, mlx, safetensors
  --json             Emit JSON output

Status flags:
  --json             Emit JSON output

Resolve flags:
  --quant <Q>        Quantization level (required for GGUF)
  --format <F>       Force format: gguf, mlx, safetensors
  --no-download      Never download, even on a miss
  --json             Emit JSON output
  --config <path>    Override config file path

Find flags:
  --format <F>       Filter results by format
  --limit <N>        Max results (default: 10)
  --json             Emit JSON output

Daemon flags:
  --port <N>         Override listen port (default: 8844)

Init flags:
  --shelf <path>     Path for model storage (required)
  --role <roles>     Node roles: controller,store,executor (required)
  --name <name>      Node name (default: hostname)
  --seed <addrs>     Comma-separated seed peer addresses (e.g. host1:8844,host2:8844)
  --key <mesh-key>   Mesh key (written to mesh.key)
  --force            Overwrite existing config

Service actions:
  install            Install and enable the service (auto-starts on login)
  uninstall          Stop and remove the service
  start              Start the service
  stop               Stop the service
  restart            Restart the service (stop + start)
  status             Show whether the service is running
`, version)
}

// booleanFlags lists flags that don't take a value argument.
var booleanFlags = map[string]bool{
	"json":        true,
	"no-download": true,
	"force":       true,
	"help":        true,
}

// parseFlags is a minimal flag parser that handles --key value, --flag, and -h style args.
// Non-boolean flags that are missing their value argument cause a fatal error.
func parseFlags(args []string) (positional []string, flags map[string]string) {
	flags = make(map[string]string)
	for i := 0; i < len(args); i++ {
		if args[i] == "-h" {
			flags["help"] = "true"
		} else if strings.HasPrefix(args[i], "--") {
			key := strings.TrimPrefix(args[i], "--")
			if booleanFlags[key] {
				flags[key] = "true"
			} else {
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
					flags[key] = args[i+1]
					i++
				} else {
					fmt.Fprintf(os.Stderr, "error: flag --%s requires a value\n", key)
					os.Exit(1)
				}
			}
		} else {
			positional = append(positional, args[i])
		}
	}
	return
}

func loadCfg(configPath string) (*resolver.Config, error) {
	return config.LoadConfig(configPath)
}

func cmdResolve(args []string) int {
	positional, flags := parseFlags(args)
	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf resolve <repo_id> [--quant Q] [--format F] [--no-download] [--json]")
		fmt.Println()
		fmt.Println("Resolve a model to a local path.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --quant <Q>        Quantization level (required for GGUF)")
		fmt.Println("  --format <F>       Force format: gguf, mlx, safetensors")
		fmt.Println("  --no-download      Never download, even on a miss")
		fmt.Println("  --json             Emit JSON output")
		fmt.Println("  --config <path>    Override config file path")
		return 0
	}
	if len(positional) < 1 {
		fmt.Fprintf(os.Stderr, "usage: model-shelf resolve <repo_id> [--quant Q] [--format F] [--no-download] [--json]\n")
		return 1
	}
	repoID := positional[0]

	cfg, err := loadCfg(flags["config"])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if _, ok := flags["no-download"]; ok {
		cfg.AllowDownloads = false
	}

	format := flags["format"]
	if format == "" {
		format = resolver.DetectFormat(repoID)
	}

	quant := flags["quant"]
	if quant != "" && format != "gguf" {
		fmt.Fprintf(os.Stderr, "warning: --quant is only used for gguf format, ignoring\n")
		quant = ""
	}

	result, err := resolver.ResolveModel(cfg, repoID, format, quant)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if flags["json"] == "true" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)
	} else {
		printResultPretty(repoID, result)
	}

	if result.Status == "missing" {
		return 1
	}

	// Update inventory last-accessed timestamp on successful resolve.
	if result.Path != nil {
		var size int64
		if info, err := os.Stat(*result.Path); err == nil {
			if info.IsDir() {
				size = dirSize(*result.Path)
			} else {
				size = info.Size()
			}
		}
		daemon.TouchModel(repoID, result.Format, quant, size)
	}

	return 0
}

func printResultPretty(repoID string, result *resolver.ResolveResult) {
	for _, c := range result.Checks {
		marker := "miss"
		if c.Result == "hit" {
			marker = "HIT"
		}
		fmt.Printf("  %-6s %-48s %s\n", c.Location, c.Root, marker)
	}
	if result.Status == "downloaded" {
		fmt.Printf("  fetch  huggingface.co/%-33s downloaded\n", repoID)
	}
	fmt.Println()
	fmt.Printf("  status      %s\n", result.Status)
	fmt.Printf("  source      %s\n", result.Source)
	fmt.Printf("  format      %s\n", result.Format)
	path := "(none)"
	if result.Path != nil {
		path = *result.Path
	}
	fmt.Printf("  path        %s\n", path)
}

func cmdInit(args []string) int {
	_, flags := parseFlags(args)

	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf init --role <roles> --shelf <path> [--name <name>] [--seed <addrs>] [--key <mesh-key>] [--force]")
		fmt.Println()
		fmt.Println("Initialize a mesh node.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --shelf <path>     Path for model storage (required)")
		fmt.Println("  --role <roles>     Node roles: controller,store,executor (required)")
		fmt.Println("  --name <name>      Node name (default: hostname)")
		fmt.Println("  --seed <addrs>     Comma-separated seed peer addresses (e.g. host1:8844,host2:8844)")
		fmt.Println("  --key <mesh-key>   Mesh key (written to mesh.key)")
		fmt.Println("  --force            Overwrite existing config")
		return 0
	}

	// --shelf is required.
	shelfPath := flags["shelf"]
	if shelfPath == "" {
		fmt.Fprintf(os.Stderr, "error: --shelf is required\n")
		fmt.Fprintf(os.Stderr, "usage: model-shelf init --role <roles> --shelf <path> [--name <name>] [--force]\n")
		return 1
	}

	// --role is required.
	roleStr := flags["role"]
	if roleStr == "" {
		fmt.Fprintf(os.Stderr, "error: --role is required\n")
		fmt.Fprintf(os.Stderr, "usage: model-shelf init --role <roles> --shelf <path> [--name <name>] [--force]\n")
		return 1
	}

	// Parse and validate roles.
	validRoles := map[string]bool{"controller": true, "store": true, "executor": true}
	seen := make(map[string]bool)
	var roles []string
	for _, r := range strings.Split(roleStr, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !validRoles[r] {
			fmt.Fprintf(os.Stderr, "error: unknown role %q (valid: controller, store, executor)\n", r)
			return 1
		}
		if !seen[r] {
			seen[r] = true
			roles = append(roles, r)
		}
	}
	if len(roles) == 0 {
		fmt.Fprintf(os.Stderr, "error: --role requires at least one valid role (controller, store, executor)\n")
		return 1
	}

	// Resolve shelf path to absolute.
	absShelf, err := filepath.Abs(shelfPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Check if config already exists (unless --force).
	force := flags["force"] == "true"
	if meshconfig.Exists() && !force {
		fmt.Fprintf(os.Stderr, "error: %s already exists (use --force to overwrite)\n", meshconfig.ConfigPath())
		return 1
	}

	// Determine node name.
	name := flags["name"]
	if name == "" {
		name = meshconfig.GetHostname()
	}

	// Create shelf directories first (fail early before writing config).
	resolverCfg := &resolver.Config{ShelfRoot: absShelf, AllowDownloads: true}
	created, err := resolver.InitShelf(resolverCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Parse seeds if provided.
	var seeds []string
	if seedStr := flags["seed"]; seedStr != "" {
		for _, s := range strings.Split(seedStr, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				seeds = append(seeds, s)
			}
		}
	}

	// Write mesh config.
	meshCfg := &meshconfig.Config{
		Name:      name,
		Port:      meshconfig.DefaultPort,
		Roles:     roles,
		ShelfRoot: absShelf,
		Seeds:     seeds,
	}
	if err := meshconfig.Write(meshCfg); err != nil {
		fmt.Fprintf(os.Stderr, "error writing config: %v\n", err)
		return 1
	}
	fmt.Printf("model-shelf: wrote %s\n", meshconfig.ConfigPath())

	// Mesh key handling:
	// 1. If --key provided, use that.
	// 2. If key already exists on disk, preserve it.
	// 3. If controller role, generate a new key.
	// 4. Otherwise, no key.
	keyPath := meshconfig.MeshKeyPath()
	if flagKey := flags["key"]; flagKey != "" {
		if err := meshconfig.WriteMeshKey(flagKey); err != nil {
			fmt.Fprintf(os.Stderr, "error writing mesh key: %v\n", err)
			return 1
		}
		fmt.Printf("model-shelf: stored mesh key at %s\n", keyPath)
	} else if keyData, err := os.ReadFile(keyPath); err == nil && len(strings.TrimSpace(string(keyData))) > 0 {
		key := strings.TrimSpace(string(keyData))
		fmt.Printf("model-shelf: existing mesh key at %s (preserved)\n", keyPath)
		fmt.Printf("\n  mesh key: %s\n\n", key)
	} else if seen["controller"] {
		key, err := meshconfig.GenerateMeshKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error generating mesh key: %v\n", err)
			return 1
		}
		fmt.Printf("model-shelf: generated mesh key at %s\n", keyPath)
		fmt.Printf("\n  mesh key: %s\n\n", key)
		fmt.Println("  Share this key with other nodes to join the mesh.")
	} else {
		fmt.Println("model-shelf: no mesh key generated (non-controller node)")
		fmt.Println("  This node will receive the mesh key when joining via: model-shelf join <peer>")
		fmt.Println("")
		fmt.Println("  ⚠ The daemon will run without mesh authentication until this node joins a mesh.")
	}

	if len(created) == 0 {
		fmt.Printf("model-shelf: shelf at %s already initialized\n", absShelf)
	} else {
		fmt.Printf("model-shelf: initialized shelf at %s\n", absShelf)
		for _, p := range created {
			fmt.Printf("  + %s\n", p)
		}
	}

	// If the daemon is running and we overwrote config, warn about restart.
	if force {
		url := fmt.Sprintf("http://127.0.0.1:%d/v1/health", meshCfg.Port)
		client := &http.Client{Timeout: 2 * time.Second}
		if resp, err := client.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				fmt.Println()
				fmt.Println("  note: daemon is running with old config — run 'model-shelf service restart' to apply changes")
			}
		}
	}

	return 0
}

func cmdFind(args []string) int {
	positional, flags := parseFlags(args)
	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf find <query> [--format F] [--limit N] [--json]")
		fmt.Println()
		fmt.Println("Search Hugging Face for models.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --format <F>       Filter results by format")
		fmt.Println("  --limit <N>        Max results (default: 10)")
		fmt.Println("  --json             Emit JSON output")
		return 0
	}
	if len(positional) < 1 {
		fmt.Fprintf(os.Stderr, "usage: model-shelf find <query> [--format F] [--limit N] [--json]\n")
		return 1
	}
	query := strings.Join(positional, " ")
	format := flags["format"]
	limit := 10
	if l, ok := flags["limit"]; ok {
		var parsed int
		if _, err := fmt.Sscanf(l, "%d", &parsed); err != nil || parsed <= 0 {
			fmt.Fprintf(os.Stderr, "error: --limit must be a positive integer, got %q\n", l)
			return 1
		}
		limit = parsed
	}

	results, err := search.FindModels(query, format, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if flags["json"] == "true" {
		if results == nil {
			results = []search.FindResult{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(results)
		if len(results) == 0 {
			return 1
		}
		return 0
	}

	if len(results) == 0 {
		fmt.Println("(no results)")
		return 1
	}
	for _, r := range results {
		fmt.Printf("  [%-11s] %-55s %10d downloads\n", r.Format, r.RepoID, r.Downloads)
	}
	return 0
}

// ShelfEntry represents a single model in the shelf for JSON output.
type ShelfEntry struct {
	RepoID    string `json:"repo_id"`
	Format    string `json:"format"`
	Quant     string `json:"quant,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	Path      string `json:"path"`
}

func cmdList(args []string) int {
	_, flags := parseFlags(args)

	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf list [--json] [--config <path>]")
		fmt.Println()
		fmt.Println("List shelf contents.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --json             Emit JSON output")
		fmt.Println("  --config <path>    Override config file path")
		return 0
	}

	cfg, err := loadCfg(flags["config"])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if err := resolver.CheckStorageAvailable(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if flags["json"] == "true" {
		entries, errs := collectShelfEntries(cfg.ShelfRoot)
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "warning: %v\n", e)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entries); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	printShelfContents(cfg.ShelfRoot)
	return 0
}

func collectShelfEntries(root string) ([]ShelfEntry, []error) {
	entries := []ShelfEntry{}
	var errs []error
	for _, format := range resolver.SupportedFormats {
		sub := filepath.Join(root, format)
		info, err := os.Stat(sub)
		if err != nil || !info.IsDir() {
			continue
		}
		publishers, err := os.ReadDir(sub)
		if err != nil {
			errs = append(errs, fmt.Errorf("reading %s: %w", sub, err))
			continue
		}
		for _, pub := range publishers {
			if !pub.IsDir() || strings.HasPrefix(pub.Name(), ".") {
				continue
			}
			pubPath := filepath.Join(sub, pub.Name())
			repos, err := os.ReadDir(pubPath)
			if err != nil {
				errs = append(errs, fmt.Errorf("reading %s: %w", pubPath, err))
				continue
			}
			for _, repo := range repos {
				if !repo.IsDir() || strings.HasPrefix(repo.Name(), ".") {
					continue
				}
				repoPath := filepath.Join(sub, pub.Name(), repo.Name())
				repoID := pub.Name() + "/" + repo.Name()

				if format == "gguf" {
					// Each .gguf file is a separate entry with its own quant.
					files, err := os.ReadDir(repoPath)
					if err != nil {
						errs = append(errs, fmt.Errorf("reading %s: %w", repoPath, err))
						continue
					}
					for _, f := range files {
						if f.IsDir() || !strings.HasSuffix(strings.ToLower(f.Name()), ".gguf") {
							continue
						}
						fInfo, err := f.Info()
						if err != nil {
							errs = append(errs, fmt.Errorf("stat %s: %w", filepath.Join(repoPath, f.Name()), err))
							continue
						}
						quant := daemon.ExtractQuant(f.Name())
						entries = append(entries, ShelfEntry{
							RepoID:    repoID,
							Format:    format,
							Quant:     quant,
							SizeBytes: fInfo.Size(),
							Path:      filepath.Join(repoPath, f.Name()),
						})
					}
				} else {
					size := dirSize(repoPath)
					entries = append(entries, ShelfEntry{
						RepoID:    repoID,
						Format:    format,
						SizeBytes: size,
						Path:      repoPath,
					})
				}
			}
		}
	}
	return entries, errs
}

func printShelfContents(root string) {
	entries, errs := collectShelfEntries(root)
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "warning: %v\n", e)
	}
	// Group entries by format for display.
	byFormat := make(map[string][]ShelfEntry)
	for _, e := range entries {
		byFormat[e.Format] = append(byFormat[e.Format], e)
	}
	for _, format := range resolver.SupportedFormats {
		fmt.Printf("\n  %s/\n", format)
		group := byFormat[format]
		if len(group) == 0 {
			fmt.Println("    (empty)")
			continue
		}
		for _, e := range group {
			fmt.Printf("    %s  %s\n", e.RepoID, fmtSize(e.SizeBytes))
		}
	}
}

func dirSize(path string) int64 {
	var total int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

func fmtSize(n int64) string {
	size := float64(n)
	for _, unit := range []string{"B", "KB", "MB", "GB", "TB"} {
		if size < 1024 || unit == "TB" {
			return fmt.Sprintf("%.1f %s", size, unit)
		}
		size /= 1024
	}
	return fmt.Sprintf("%.1f TB", size)
}

func cmdDaemon(args []string) int {
	_, flags := parseFlags(args)

	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf daemon [--port <N>]")
		fmt.Println()
		fmt.Println("Start the mesh daemon (foreground).")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --port <N>         Override listen port (default: 8844)")
		return 0
	}

	// Load mesh config.
	if !meshconfig.Exists() {
		fmt.Fprintf(os.Stderr, "error: not part of a mesh — run `model-shelf init` and `model-shelf join`\n")
		return 1
	}
	cfg, err := meshconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Validate port from config.
	if cfg.Port <= 0 || cfg.Port > 65535 {
		fmt.Fprintf(os.Stderr, "error: invalid port %d in config (must be 1-65535)\n", cfg.Port)
		return 1
	}

	// Override port from flag.
	if portStr, ok := flags["port"]; ok {
		var port int
		if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port <= 0 || port > 65535 {
			fmt.Fprintf(os.Stderr, "error: --port must be a valid port number (1-65535), got %q\n", portStr)
			return 1
		}
		cfg.Port = port
	}

	d := daemon.New(cfg)
	if err := d.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func cmdService(args []string) int {
	if len(args) < 1 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: model-shelf service <install|uninstall|start|stop|restart|status>")
		fmt.Println()
		fmt.Println("Manage the system service.")
		fmt.Println()
		fmt.Println("Actions:")
		fmt.Println("  install     Install and enable the service (auto-starts on login)")
		fmt.Println("  uninstall   Stop and remove the service")
		fmt.Println("  start       Start the service")
		fmt.Println("  stop        Stop the service")
		fmt.Println("  restart     Restart the service (stop + start)")
		fmt.Println("  status      Show whether the service is running")
		if len(args) < 1 {
			return 1
		}
		return 0
	}

	action := args[0]
	switch action {
	case "install":
		if err := service.Install(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println("model-shelf: service installed, enabled, and started")
	case "uninstall":
		if err := service.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println("model-shelf: service removed")
	case "start":
		if err := service.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println("model-shelf: service started")
	case "stop":
		if err := service.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println("model-shelf: service stopped")
	case "restart":
		if err := service.Restart(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println("model-shelf: service restarted")
	case "status":
		status, err := service.GetStatus()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("model-shelf: %s\n", status)
		if status.Detail != "" {
			fmt.Printf("\n%s\n", status.Detail)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown service action: %s\n", action)
		fmt.Fprintf(os.Stderr, "usage: model-shelf service <install|uninstall|start|stop|restart|status>\n")
		return 1
	}
	return 0
}
