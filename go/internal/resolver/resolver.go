// Package resolver implements core model resolution logic.
package resolver

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SupportedFormats lists the model formats handled by Model Shelf.
var SupportedFormats = []string{"gguf", "mlx", "safetensors"}

// SafetensorsAllowPatterns is the file filter applied when downloading safetensors repos.
var SafetensorsAllowPatterns = []string{
	"*.safetensors",
	"*.safetensors.index.json",
	"*.json",
	"tokenizer*",
	"*.txt",
	"*.md",
}

// Config holds the runtime configuration after auto-discovery has run.
// ExtraRoots lists additional shelf roots to check during resolve (secondary drives).
type Config struct {
	ShelfRoot      string
	ExtraRoots     []string
	AllowDownloads bool
}

// Check records a single shelf lookup attempt.
type Check struct {
	Location string `json:"location"`
	Root     string `json:"root"`
	Result   string `json:"result"`
}

// ResolveResult is returned by ResolveLocal.
type ResolveResult struct {
	Status string  `json:"status"` // "found" | "missing"
	Source string  `json:"source"` // "local_shelf" | "none"
	Format string  `json:"format"`
	Path   string  `json:"path,omitempty"`
	Checks []Check `json:"checks"`
}

// StorageNotAvailableError is returned when the shelf volume is unmounted or missing.
type StorageNotAvailableError struct{ Message string }

func (e *StorageNotAvailableError) Error() string { return e.Message }

// ShelfNotInitializedError is returned when shelf_root doesn't exist yet.
type ShelfNotInitializedError struct{ Message string }

func (e *ShelfNotInitializedError) Error() string { return e.Message }

// DetectFormat returns the format based on repo ID heuristics.
func DetectFormat(repoID string) string {
	parts := strings.Split(repoID, "/")
	name := strings.ToLower(parts[len(parts)-1])
	re := regexp.MustCompile(`[-_./]`)
	tokens := make(map[string]bool)
	for _, t := range re.Split(name, -1) {
		if t != "" {
			tokens[t] = true
		}
	}
	if tokens["gguf"] {
		return "gguf"
	}
	org := ""
	if len(parts) > 1 {
		org = strings.ToLower(parts[0])
	}
	if org == "mlx-community" || tokens["mlx"] {
		return "mlx"
	}
	return "safetensors"
}

// HFFilename returns a best-guess filename for a GGUF file in a HuggingFace repo.
// Strips the -GGUF suffix and avoids doubling the quant tag.
// Use FindLocalGGUF for local lookups and hf.FindGGUFFilename for remote lookups,
// which both handle repos that use "." instead of "-" as the separator.
func HFFilename(repoID, quant string) string {
	name := repoID[strings.LastIndex(repoID, "/")+1:]
	if strings.HasSuffix(strings.ToLower(name), "-gguf") {
		name = name[:len(name)-5]
	}
	if strings.Contains(strings.ToLower(name), strings.ToLower(quant)) {
		return name + ".gguf"
	}
	return name + "-" + quant + ".gguf"
}

// ShelfPathGGUF returns the shelf path for a GGUF file using HFFilename guessing.
func ShelfPathGGUF(shelfRoot, repoID, quant string) string {
	publisher, repo := splitRepoID(repoID)
	return filepath.Join(shelfRoot, "gguf", publisher, repo, HFFilename(repoID, quant))
}

// ShelfPathGGUFFile returns the shelf path for a GGUF file with an explicit filename.
func ShelfPathGGUFFile(shelfRoot, repoID, filename string) string {
	publisher, repo := splitRepoID(repoID)
	return filepath.Join(shelfRoot, "gguf", publisher, repo, filename)
}

// FindLocalGGUF scans the shelf directory for any .gguf file whose name
// contains quant (case-insensitive). Returns the full path if found, else "".
// This handles repos where the separator before the quant tag is "." instead of "-".
func FindLocalGGUF(shelfRoot, repoID, quant string) string {
	publisher, repo := splitRepoID(repoID)
	dir := filepath.Join(shelfRoot, "gguf", publisher, repo)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	ql := strings.ToLower(quant)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		nl := strings.ToLower(name)
		if strings.HasSuffix(nl, ".gguf") && strings.Contains(nl, ql) {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// ShelfPathSnapshot returns the shelf directory for mlx/safetensors.
func ShelfPathSnapshot(shelfRoot, repoID, format string) string {
	publisher, repo := splitRepoID(repoID)
	return filepath.Join(shelfRoot, format, publisher, repo)
}

// CheckStorageAvailable returns an error if shelf_root is missing or unmounted.
func CheckStorageAvailable(cfg *Config) error {
	if cfg.ShelfRoot == "" {
		return &ShelfNotInitializedError{Message: "no shelf_root configured; run `model-shelf init`"}
	}
	if !isDir(cfg.ShelfRoot) {
		return &ShelfNotInitializedError{
			Message: fmt.Sprintf(
				"shelf_root is set to %s but that folder doesn't exist.\n"+
					"Run `model-shelf init` to create it, or check your config.",
				cfg.ShelfRoot,
			),
		}
	}
	return nil
}

// InitShelf creates shelf_root and format subdirectories. Returns newly-created paths.
func InitShelf(cfg *Config) ([]string, error) {
	var created []string
	dirs := []string{cfg.ShelfRoot}
	for _, format := range SupportedFormats {
		dirs = append(dirs, filepath.Join(cfg.ShelfRoot, format))
	}
	for _, d := range dirs {
		if !isDir(d) {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return created, err
			}
			created = append(created, d)
		}
	}
	return created, nil
}

// ListCandidates returns all shelf roots to check during resolve.
// Primary (cfg.ShelfRoot) is always first, followed by ExtraRoots.
// Deduplicates by resolved path.
func ListCandidates(cfg *Config) []string {
	seen := make(map[string]bool)
	var out []string

	add := func(p string) {
		if p == "" {
			return
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			resolved = p
		}
		if !seen[resolved] {
			seen[resolved] = true
			out = append(out, p)
		}
	}

	add(cfg.ShelfRoot)
	for _, r := range cfg.ExtraRoots {
		add(r)
	}
	return out
}

// ResolveLocal checks all shelf candidates for the model. Does NOT download.
// Returns status "found" with Path set, or status "missing".
func ResolveLocal(cfg *Config, repoID, format, quant string) (*ResolveResult, error) {
	if err := CheckStorageAvailable(cfg); err != nil {
		return nil, err
	}
	var checks []Check
	for _, root := range ListCandidates(cfg) {
		shelfDir := filepath.Join(root, format)
		var hitPath string
		if format == "gguf" {
			// Scan the directory to match any separator convention ("." or "-").
			hitPath = FindLocalGGUF(root, repoID, quant)
		} else {
			candidate := ShelfPathSnapshot(root, repoID, format)
			if isHit(candidate, format) {
				hitPath = candidate
			}
		}
		if hitPath != "" {
			checks = append(checks, Check{Location: "shelf", Root: shelfDir, Result: "hit"})
			return &ResolveResult{
				Status: "found", Source: "local_shelf", Format: format,
				Path: hitPath, Checks: checks,
			}, nil
		}
		checks = append(checks, Check{Location: "shelf", Root: shelfDir, Result: "miss"})
	}
	return &ResolveResult{Status: "missing", Source: "none", Format: format, Checks: checks}, nil
}

func isHit(path, format string) bool {
	if format == "gguf" {
		_, err := os.Stat(path)
		return err == nil
	}
	// mlx / safetensors: directory with config.json.
	_, err := os.Stat(filepath.Join(path, "config.json"))
	return err == nil
}

func splitRepoID(repoID string) (publisher, repo string) {
	idx := strings.Index(repoID, "/")
	if idx < 0 {
		return "", repoID
	}
	return repoID[:idx], repoID[idx+1:]
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
