package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bmaltais/model-shelf/internal/config"
	"github.com/bmaltais/model-shelf/internal/detect"
	"github.com/bmaltais/model-shelf/internal/hf"
	"github.com/bmaltais/model-shelf/internal/relocate"
	"github.com/bmaltais/model-shelf/internal/resolver"
)

var reQuantTag = regexp.MustCompile(`(?i)\b(IQ\d+(?:_[A-Z0-9]+)*|Q\d+(?:_[A-Z0-9]+)*|BF16|F16|F32)\b`)

// detectQuantFromFilename extracts a quant tag from a GGUF filename, e.g. "Q4_K_M" from
// "Model.Q4_K_M.gguf". Returns "" if no known quant pattern is found.
func detectQuantFromFilename(filename string) string {
	if m := reQuantTag.FindString(filename); m != "" {
		return strings.ToUpper(m)
	}
	return ""
}

// parseHFURL detects a full HuggingFace URL and extracts (repoID, filename, cleanURL).
// Returns ok=false for non-URL arguments.
func parseHFURL(arg string) (repoID, filename, cleanURL string, ok bool) {
	const hfBase = "https://huggingface.co/"
	var path string
	switch {
	case strings.HasPrefix(arg, hfBase):
		path = strings.TrimPrefix(arg, hfBase)
	case strings.HasPrefix(arg, "http://huggingface.co/"):
		path = strings.TrimPrefix(arg, "http://huggingface.co/")
	default:
		return "", "", "", false
	}
	// Strip query string.
	if idx := strings.IndexByte(path, '?'); idx >= 0 {
		path = path[:idx]
	}
	// Expect: {owner}/{repo}/resolve/{branch}/{filename}
	parts := strings.SplitN(path, "/", 5)
	if len(parts) != 5 || parts[2] != "resolve" {
		return "", "", "", false
	}
	repoID = parts[0] + "/" + parts[1]
	filename = parts[4]
	cleanURL = hfBase + repoID + "/resolve/" + parts[3] + "/" + filename
	return repoID, filename, cleanURL, true
}

func resolveCmd() *cobra.Command {
	var (
		format     string
		quant      string
		noDownload bool
		jsonOut    bool
	)

	cmd := &cobra.Command{
		Use:   "resolve <repo_id>",
		Short: "Resolve a model to a local path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoID := args[0]

			// Accept full HF URLs: parse out repo ID, filename, and direct URL.
			var directURL, directFilename string
			if parsedRepo, parsedFile, parsedURL, ok := parseHFURL(repoID); ok {
				repoID = parsedRepo
				directFilename = parsedFile
				directURL = parsedURL
				if format == "" && strings.HasSuffix(strings.ToLower(parsedFile), ".gguf") {
					format = "gguf"
				}
				if format == "gguf" && quant == "" {
					if q := detectQuantFromFilename(parsedFile); q != "" {
						quant = q
					}
				}
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			if format == "" {
				format = resolver.DetectFormat(repoID)
			}
			if format == "gguf" && quant == "" {
				return fmt.Errorf("--quant is required for gguf format")
			}

			result, err := resolver.ResolveLocal(cfg, repoID, format, quant)
			if err != nil {
				return err
			}

			if result.Status == "found" {
				printResolveResult(repoID, result, jsonOut)
				return nil
			}

			// Miss path.
			if noDownload || !cfg.AllowDownloads {
				printResolveResult(repoID, result, jsonOut)
				os.Exit(1)
			}

			// Download.
			var destPath string
			dlOpts := hf.DownloadOptions{NoProgress: jsonOut}

			if format == "gguf" {
				// Determine actual filename: prefer URL-parsed name, then repo listing,
				// then fall back to the HFFilename best-guess.
				filename := directFilename
				if filename == "" {
					if f, err := hf.FindGGUFFilename(repoID, quant, dlOpts.Token); err == nil && f != "" {
						filename = f
					} else {
						filename = resolver.HFFilename(repoID, quant)
					}
				}
				destPath = resolver.ShelfPathGGUFFile(cfg.ShelfRoot, repoID, filename)
				url := directURL
				if url == "" {
					url = fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repoID, filename)
				}
				if err := hf.DownloadFile(url, destPath, dlOpts); err != nil {
					return fmt.Errorf("download failed: %w", err)
				}
			} else {
				destPath = resolver.ShelfPathSnapshot(cfg.ShelfRoot, repoID, format)
				var patterns []string
				if format == "safetensors" {
					patterns = resolver.SafetensorsAllowPatterns
				}
				if err := hf.DownloadSnapshot(repoID, destPath, hf.SnapshotOptions{
					AllowPatterns: patterns,
					NoProgress:    jsonOut,
				}); err != nil {
					return fmt.Errorf("snapshot download failed: %w", err)
				}
			}

			downloaded := &resolver.ResolveResult{
				Status: "downloaded",
				Source: "huggingface",
				Format: format,
				Path:   destPath,
				Checks: result.Checks,
			}
			printResolveResult(repoID, downloaded, jsonOut)
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "", "model format (gguf, mlx, safetensors; auto-detected if omitted)")
	cmd.Flags().StringVar(&quant, "quant", "", "quantization tag, required for gguf (e.g. Q4_K_M)")
	cmd.Flags().BoolVar(&noDownload, "no-download", false, "never reach out to Hugging Face")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func printResolveResult(repoID string, r *resolver.ResolveResult, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(r)
		return
	}
	for _, c := range r.Checks {
		marker := "miss"
		if c.Result == "hit" {
			marker = "HIT"
		}
		fmt.Printf("  %-6s %-48s %s\n", c.Location, c.Root, marker)
	}
	if r.Status == "downloaded" {
		fmt.Printf("  %-6s huggingface.co/%-33s downloaded\n", "fetch", repoID)
	}
	fmt.Println()
	fmt.Printf("  %-12s %s\n", "status", r.Status)
	fmt.Printf("  %-12s %s\n", "source", r.Source)
	fmt.Printf("  %-12s %s\n", "format", r.Format)
	fmt.Printf("  %-12s %s\n", "path", r.Path)
}

// loadConfig is shared by all subcommands.
func loadConfig() (*resolver.Config, error) {
	cfg, err := config.LoadRaw(cfgPath)
	if err != nil {
		return nil, err
	}

	candidates := detect.Candidates()

	var primary string
	if cfg.ShelfRoot != "" {
		primary = relocate.Shelf(cfg.ShelfRoot, "")
	} else {
		// Auto-discover primary: first existing candidate, else internal fallback.
		for _, c := range candidates {
			if c.Existing {
				primary = c.Path
				break
			}
		}
		if primary == "" && len(candidates) > 0 {
			primary = candidates[len(candidates)-1].Path // internal fallback
		}
	}

	// Collect all other existing shelf candidates as extra roots.
	var extra []string
	for _, c := range candidates {
		if c.Existing && c.Path != primary {
			extra = append(extra, c.Path)
		}
	}

	return &resolver.Config{
		ShelfRoot:      primary,
		ExtraRoots:     extra,
		AllowDownloads: cfg.AllowDownloads,
	}, nil
}
