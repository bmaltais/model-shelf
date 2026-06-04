// Command model-shelf is a local-first resolver for Hugging Face models.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexziskind1/model-shelf/internal/config"
	"github.com/alexziskind1/model-shelf/internal/detect"
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
  model-shelf init [path]              Initialize shelf (auto-detect or explicit path)
  model-shelf resolve <repo_id>        Resolve a model to a local path
  model-shelf find <query>             Search Hugging Face for models
  model-shelf list                     List shelf contents
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
`, version)
}

// parseFlags is a minimal flag parser that handles --key value and --flag style args.
func parseFlags(args []string) (positional []string, flags map[string]string) {
	flags = make(map[string]string)
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			key := strings.TrimPrefix(args[i], "--")
			// Boolean flags (no value)
			switch key {
			case "json", "no-download":
				flags[key] = "true"
			default:
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
					flags[key] = args[i+1]
					i++
				} else {
					flags[key] = "true"
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
	positional, flags := parseFlags(args)

	cfg, err := loadCfg(flags["config"])
	if err != nil {
		// Config might not exist yet — that's fine for init.
		cfg = &resolver.Config{AllowDownloads: true}
	}

	var chosenPath string
	if len(positional) > 0 {
		// Explicit path provided.
		p, err := filepath.Abs(positional[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		chosenPath = p
	} else {
		// Auto-detect.
		chosenPath = resolveShelfPathAuto()
		if chosenPath == "" {
			fmt.Fprintf(os.Stderr, "error: could not determine a shelf location\n")
			return 1
		}
	}

	cfg.ShelfRoot = chosenPath
	created, err := resolver.InitShelf(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	configPath := config.WritableConfigPath(flags["config"])
	if len(positional) > 0 {
		// Explicit path → pin.
		if err := config.WriteConfig(configPath, chosenPath, cfg.AllowDownloads); err != nil {
			fmt.Fprintf(os.Stderr, "error writing config: %v\n", err)
			return 1
		}
		fmt.Printf("model-shelf: wrote %s\n", configPath)
		fmt.Printf("            shelf_root = %s  (pinned)\n", chosenPath)
	} else {
		fmt.Printf("model-shelf: using %s  (auto-discovered, not pinned in config)\n", chosenPath)
	}

	if len(created) == 0 {
		fmt.Printf("model-shelf: shelf at %s already initialized\n", cfg.ShelfRoot)
		return 0
	}
	fmt.Printf("model-shelf: initialized shelf at %s\n", cfg.ShelfRoot)
	for _, p := range created {
		fmt.Printf("  + %s\n", p)
	}
	return 0
}

func resolveShelfPathAuto() string {
	candidates := detect.DetectStorageCandidates()

	// Single existing external → use it.
	var existingExternal []detect.StorageCandidate
	for _, c := range candidates {
		if c.Existing && c.IsExternal {
			existingExternal = append(existingExternal, c)
		}
	}
	if len(existingExternal) == 1 {
		fmt.Printf("model-shelf: using existing shelf at %s\n", existingExternal[0].Path)
		return existingExternal[0].Path
	}
	if len(existingExternal) > 1 {
		fmt.Fprintf(os.Stderr, "error: multiple existing external shelves; specify a path explicitly:\n")
		for _, c := range existingExternal {
			fmt.Fprintf(os.Stderr, "  - %s\n", c.Path)
		}
		return ""
	}

	// No existing external — check if any external drives exist.
	var external []detect.StorageCandidate
	for _, c := range candidates {
		if c.IsExternal {
			external = append(external, c)
		}
	}

	// Find internal candidate.
	var internal *detect.StorageCandidate
	for i := range candidates {
		if !candidates[i].IsExternal {
			internal = &candidates[i]
			break
		}
	}

	if len(external) == 0 {
		if internal == nil {
			return ""
		}
		fmt.Printf("model-shelf: no external drives detected; using internal storage at %s\n", internal.Path)
		return internal.Path
	}

	// Non-interactive: use internal.
	if internal != nil {
		fmt.Fprintf(os.Stderr, "model-shelf: not interactive; using internal storage at %s\n", internal.Path)
		return internal.Path
	}
	return ""
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
		fmt.Sscanf(l, "%d", &limit)
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
