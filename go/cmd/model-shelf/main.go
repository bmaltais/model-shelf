// Command model-shelf is a local-first resolver for Hugging Face models.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexziskind1/model-shelf/internal/config"
	"github.com/alexziskind1/model-shelf/internal/daemon"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
	"github.com/alexziskind1/model-shelf/internal/resolver"
	"github.com/alexziskind1/model-shelf/internal/search"
)

const version = "0.13.1"

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
  model-shelf resolve <repo_id>        Resolve a model to a local path
  model-shelf find <query>             Search Hugging Face for models
  model-shelf list                     List shelf contents
  model-shelf daemon                   Start the mesh daemon (foreground)
  model-shelf version                  Print version

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
  --force            Overwrite existing config
`, version)
}

// booleanFlags lists flags that don't take a value argument.
var booleanFlags = map[string]bool{
	"json":        true,
	"no-download": true,
	"force":       true,
}

// parseFlags is a minimal flag parser that handles --key value and --flag style args.
// Non-boolean flags that are missing their value argument cause a fatal error.
func parseFlags(args []string) (positional []string, flags map[string]string) {
	flags = make(map[string]string)
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
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

	result, err := resolver.ResolveModel(cfg, repoID, format, flags["quant"])
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

	// Write mesh config.
	meshCfg := &meshconfig.Config{
		Name:      name,
		Port:      meshconfig.DefaultPort,
		Roles:     roles,
		ShelfRoot: absShelf,
	}
	if err := meshconfig.Write(meshCfg); err != nil {
		fmt.Fprintf(os.Stderr, "error writing config: %v\n", err)
		return 1
	}
	fmt.Printf("model-shelf: wrote %s\n", meshconfig.ConfigPath())

	// Generate mesh key only if one doesn't already exist.
	keyPath := meshconfig.MeshKeyPath()
	if keyData, err := os.ReadFile(keyPath); err == nil && len(strings.TrimSpace(string(keyData))) > 0 {
		key := strings.TrimSpace(string(keyData))
		fmt.Printf("model-shelf: existing mesh key at %s (preserved)\n", keyPath)
		fmt.Printf("\n  mesh key: %s\n\n", key)
	} else {
		key, err := meshconfig.GenerateMeshKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error generating mesh key: %v\n", err)
			return 1
		}
		fmt.Printf("model-shelf: generated mesh key at %s\n", keyPath)
		fmt.Printf("\n  mesh key: %s\n\n", key)
		fmt.Println("  Share this key with other nodes to join the mesh.")
	}

	if len(created) == 0 {
		fmt.Printf("model-shelf: shelf at %s already initialized\n", absShelf)
	} else {
		fmt.Printf("model-shelf: initialized shelf at %s\n", absShelf)
		for _, p := range created {
			fmt.Printf("  + %s\n", p)
		}
	}
	return 0
}

func cmdFind(args []string) int {
	positional, flags := parseFlags(args)
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

func cmdList(args []string) int {
	_, flags := parseFlags(args)

	cfg, err := loadCfg(flags["config"])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if err := resolver.CheckStorageAvailable(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	printShelfContents(cfg.ShelfRoot)
	return 0
}

func printShelfContents(root string) {
	for _, format := range resolver.SupportedFormats {
		sub := filepath.Join(root, format)
		fmt.Printf("\n  %s/\n", format)
		info, err := os.Stat(sub)
		if err != nil || !info.IsDir() {
			fmt.Println("    (empty)")
			continue
		}
		publishers, err := os.ReadDir(sub)
		if err != nil {
			fmt.Println("    (error reading)")
			continue
		}
		hasContent := false
		for _, pub := range publishers {
			if !pub.IsDir() || strings.HasPrefix(pub.Name(), ".") {
				continue
			}
			repos, err := os.ReadDir(filepath.Join(sub, pub.Name()))
			if err != nil {
				continue
			}
			for _, repo := range repos {
				if !repo.IsDir() || strings.HasPrefix(repo.Name(), ".") {
					continue
				}
				repoPath := filepath.Join(sub, pub.Name(), repo.Name())
				size := dirSize(repoPath)
				fmt.Printf("    %s/%s  %s\n", pub.Name(), repo.Name(), fmtSize(size))
				hasContent = true
			}
		}
		if !hasContent {
			fmt.Println("    (empty)")
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

	// Load mesh config.
	if !meshconfig.Exists() {
		fmt.Fprintf(os.Stderr, "error: mesh not configured. Create %s with node name, roles, and shelf_root.\n", meshconfig.ConfigPath())
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
