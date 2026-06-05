// Package resolver implements the core model resolution logic.
//
// Supported formats:
//
//	gguf         single .gguf file       (llama.cpp / Ollama / LM Studio)
//	mlx          directory of files      (MLX, MLX-LM)
//	safetensors  directory of files      (transformers, vLLM, exllamav2)
//
// Lookup order:
//  1. Curated shelf      (shelf_root / <format> / ...)
//  2. Download from Hugging Face directly into the shelf (if allow_downloads).
package resolver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// SupportedFormats lists the model formats handled by Model Shelf.
var SupportedFormats = []string{"gguf", "mlx", "safetensors"}

// ShelfLeafName is the convention used by detect_storage_candidates.
const ShelfLeafName = "ModelShelf/models"

// SafetensorsAllowPatterns lists file patterns to download for safetensors repos.
var SafetensorsAllowPatterns = []string{
	"*.safetensors",
	"*.safetensors.index.json",
	"*.json",
	"tokenizer*",
	"*.txt",
	"*.md",
}

// Config holds the runtime configuration.
type Config struct {
	ShelfRoot      string
	AllowDownloads bool
}

// Check represents a single shelf lookup attempt in the resolve log.
type Check struct {
	Location string `json:"location"`
	Root     string `json:"root"`
	Result   string `json:"result"`
}

// ResolveResult is returned by ResolveModel.
type ResolveResult struct {
	Status string  `json:"status"` // "found" | "downloaded" | "missing"
	Source string  `json:"source"` // "local_shelf" | "huggingface" | "none"
	Format string  `json:"format"` // "gguf" | "mlx" | "safetensors"
	Path   *string `json:"path"`   // file for gguf, directory for mlx/safetensors
	Checks []Check `json:"checks"`
}

// StorageNotAvailableError is returned when the shelf volume is unmounted.
type StorageNotAvailableError struct {
	Message string
}

func (e *StorageNotAvailableError) Error() string { return e.Message }

// ShelfNotInitializedError is returned when shelf_root doesn't exist.
type ShelfNotInitializedError struct {
	Message string
}

func (e *ShelfNotInitializedError) Error() string { return e.Message }

// DetectFormat returns a format string based on repo ID heuristics.
func DetectFormat(repoID string) string {
	parts := strings.Split(repoID, "/")
	name := strings.ToLower(parts[len(parts)-1])
	re := regexp.MustCompile(`[-_./]`)
	tokens := re.Split(name, -1)
	tokenSet := make(map[string]bool)
	for _, t := range tokens {
		if t != "" {
			tokenSet[t] = true
		}
	}

	if tokenSet["gguf"] {
		return "gguf"
	}

	org := ""
	if len(parts) > 1 {
		org = strings.ToLower(parts[0])
	}
	if org == "mlx-community" || tokenSet["mlx"] {
		return "mlx"
	}

	return "safetensors"
}

// splitRepoID splits "publisher/repo" into its parts.
func splitRepoID(repoID string) (string, string, error) {
	idx := strings.Index(repoID, "/")
	if idx < 0 {
		return "", "", fmt.Errorf("repo_id must be in 'publisher/repo' format (e.g. Qwen/Qwen3-14B-GGUF), got: %q", repoID)
	}
	return repoID[:idx], repoID[idx+1:], nil
}

// HFFilename returns the expected filename in a Hugging Face GGUF repo.
func HFFilename(repoID, quant string) string {
	parts := strings.Split(repoID, "/")
	name := parts[len(parts)-1]
	if strings.HasSuffix(strings.ToLower(name), "-gguf") {
		name = name[:len(name)-5]
	}
	if strings.Contains(strings.ToLower(name), strings.ToLower(quant)) {
		return name + ".gguf"
	}
	return name + "-" + quant + ".gguf"
}

// ShelfPathGGUF returns the shelf path for a GGUF model file.
func ShelfPathGGUF(shelfRoot, repoID, quant string) (string, error) {
	publisher, repo, err := splitRepoID(repoID)
	if err != nil {
		return "", err
	}
	return filepath.Join(shelfRoot, "gguf", publisher, repo, HFFilename(repoID, quant)), nil
}

// ShelfPathSnapshot returns the shelf path for an mlx/safetensors model directory.
func ShelfPathSnapshot(shelfRoot, repoID, format string) (string, error) {
	publisher, repo, err := splitRepoID(repoID)
	if err != nil {
		return "", err
	}
	return filepath.Join(shelfRoot, format, publisher, repo), nil
}

// looksLikeModelDir checks if a path is a directory containing config.json.
func looksLikeModelDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(path, "config.json"))
	return err == nil
}

// volumesDir returns the platform-specific volumes directory.
func volumesDir() string {
	if runtime.GOOS == "darwin" {
		return "/Volumes"
	}
	// Linux/Windows: no standard volumes dir; rely on config.
	return ""
}

// ListShelfCandidates returns every plausible shelf root to search.
func ListShelfCandidates(cfg *Config) []string {
	seen := make(map[string]bool)
	var out []string

	add := func(p string) {
		resolved, err := filepath.Abs(p)
		if err != nil {
			resolved = p
		}
		if seen[resolved] {
			return
		}
		seen[resolved] = true
		out = append(out, p)
	}

	if cfg.ShelfRoot != "" {
		add(cfg.ShelfRoot)
	}

	vDir := volumesDir()
	if vDir != "" {
		if entries, err := os.ReadDir(vDir); err == nil {
			sort.Slice(entries, func(i, j int) bool {
				return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
			})
			for _, e := range entries {
				if e.Type()&os.ModeSymlink != 0 {
					continue
				}
				candidate := filepath.Join(vDir, e.Name(), "ModelShelf", "models")
				if info, err := os.Stat(candidate); err == nil && info.IsDir() {
					add(candidate)
				}
			}
		}
	}

	home, err := os.UserHomeDir()
	if err == nil {
		internal := filepath.Join(home, ".cache", "model-shelf", "models")
		if info, err := os.Stat(internal); err == nil && info.IsDir() {
			add(internal)
		}
	}

	return out
}

// DiscoverPrimaryShelf picks a default primary shelf when config doesn't pin one.
func DiscoverPrimaryShelf() string {
	vDir := volumesDir()
	if vDir != "" {
		if entries, err := os.ReadDir(vDir); err == nil {
			sort.Slice(entries, func(i, j int) bool {
				return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
			})
			for _, e := range entries {
				if e.Type()&os.ModeSymlink != 0 {
					continue
				}
				candidate := filepath.Join(vDir, e.Name(), "ModelShelf", "models")
				if info, err := os.Stat(candidate); err == nil && info.IsDir() {
					return candidate
				}
			}
		}
	}

	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "model-shelf", "models")
}

// CheckStorageAvailable verifies the shelf is accessible.
func CheckStorageAvailable(cfg *Config) error {
	if cfg.ShelfRoot == "" {
		return &ShelfNotInitializedError{Message: "shelf_root is not set. Run `model-shelf init` to create your shelf."}
	}
	// macOS volume mount check
	if runtime.GOOS == "darwin" {
		parts := strings.Split(filepath.Clean(cfg.ShelfRoot), string(filepath.Separator))
		if len(parts) >= 3 && parts[0] == "" && parts[1] == "Volumes" {
			volume := filepath.Join("/", "Volumes", parts[2])
			if _, err := os.Stat(volume); err != nil {
				return &StorageNotAvailableError{
					Message: fmt.Sprintf("shelf_root is set to %s\nbut the volume '%s' is not mounted.\nPlug the drive in, or update your config.", cfg.ShelfRoot, volume),
				}
			}
		}
	}
	if info, err := os.Stat(cfg.ShelfRoot); err != nil || !info.IsDir() {
		return &ShelfNotInitializedError{
			Message: fmt.Sprintf("shelf_root is set to %s\nbut that folder doesn't exist. Run `model-shelf init` to create it.", cfg.ShelfRoot),
		}
	}
	return nil
}

// InitShelf creates shelf_root + format subfolders. Returns newly-created paths.
func InitShelf(cfg *Config) ([]string, error) {
	if cfg.ShelfRoot == "" {
		return nil, fmt.Errorf("shelf_root is not set")
	}
	// macOS volume mount check
	if runtime.GOOS == "darwin" {
		parts := strings.Split(filepath.Clean(cfg.ShelfRoot), string(filepath.Separator))
		if len(parts) >= 3 && parts[0] == "" && parts[1] == "Volumes" {
			volume := filepath.Join("/", "Volumes", parts[2])
			if _, err := os.Stat(volume); err != nil {
				return nil, &StorageNotAvailableError{
					Message: fmt.Sprintf("shelf_root is set to %s\nbut the volume '%s' is not mounted.", cfg.ShelfRoot, volume),
				}
			}
		}
	}

	// Check that no ancestor of the shelf path is a regular file.
	if err := checkPathAncestors(cfg.ShelfRoot); err != nil {
		return nil, err
	}

	var created []string
	dirs := []string{cfg.ShelfRoot}
	for _, fmt := range SupportedFormats {
		dirs = append(dirs, filepath.Join(cfg.ShelfRoot, fmt))
	}
	for _, d := range dirs {
		if _, err := os.Stat(d); err != nil {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return created, err
			}
			created = append(created, d)
		}
	}
	return created, nil
}

// checkPathAncestors walks from root toward the target path, checking for
// regular files that would block directory creation. Returns an actionable
// error if a file is found at any component.
func checkPathAncestors(target string) error {
	clean := filepath.Clean(target)
	// Walk each component from the root toward the target.
	dir := clean
	var components []string
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		components = append([]string{dir}, components...)
		dir = parent
	}
	for _, component := range components {
		info, err := os.Stat(component)
		if err != nil {
			if os.IsNotExist(err) {
				// Doesn't exist yet — ancestors above were fine, mkdir will work.
				return nil
			}
			// Permission error or other real problem — surface it.
			return fmt.Errorf("cannot create shelf at %s: stat %s: %w", target, component, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("cannot create shelf at %s: %s exists but is not a directory", target, component)
		}
	}
	return nil
}

// ResolveModel resolves a model to a local path, optionally downloading it.
func ResolveModel(cfg *Config, repoID string, format string, quant string) (*ResolveResult, error) {
	if err := CheckStorageAvailable(cfg); err != nil {
		return nil, err
	}
	fmt := format
	if fmt == "" {
		fmt = DetectFormat(repoID)
	}
	valid := false
	for _, f := range SupportedFormats {
		if f == fmt {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt2("unsupported format: %q", fmt)
	}
	if fmt == "gguf" && quant == "" {
		return nil, fmt2("--quant is required for gguf format")
	}

	if fmt == "gguf" {
		return resolveGGUF(cfg, repoID, quant)
	}
	return resolveSnapshot(cfg, repoID, fmt)
}

func fmt2(format string, args ...interface{}) error {
	return fmt.Errorf(format, args...)
}

func resolveGGUF(cfg *Config, repoID, quant string) (*ResolveResult, error) {
	var checks []Check

	for _, parent := range ListShelfCandidates(cfg) {
		candidate, err := ShelfPathGGUF(parent, repoID, quant)
		if err != nil {
			return nil, err
		}
		shelf := filepath.Join(parent, "gguf")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			checks = append(checks, Check{Location: "shelf", Root: shelf, Result: "hit"})
			path := candidate
			return &ResolveResult{Status: "found", Source: "local_shelf", Format: "gguf", Path: &path, Checks: checks}, nil
		}
		checks = append(checks, Check{Location: "shelf", Root: shelf, Result: "miss"})
	}

	if !cfg.AllowDownloads {
		return &ResolveResult{Status: "missing", Source: "none", Format: "gguf", Path: nil, Checks: checks}, nil
	}

	// Download into primary shelf.
	finalPath, err := ShelfPathGGUF(cfg.ShelfRoot, repoID, quant)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(finalPath)
	dirExisted := dirExists(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	hfName := HFFilename(repoID, quant)
	url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repoID, hfName)
	if err := downloadFile(url, finalPath); err != nil {
		os.Remove(finalPath) // remove partial/corrupt file
		cleanupOnFailure(dir, dirExisted)
		return nil, fmt.Errorf("download failed: %w", err)
	}

	return &ResolveResult{Status: "downloaded", Source: "huggingface", Format: "gguf", Path: &finalPath, Checks: checks}, nil
}

func resolveSnapshot(cfg *Config, repoID, format string) (*ResolveResult, error) {
	var checks []Check

	for _, parent := range ListShelfCandidates(cfg) {
		candidate, err := ShelfPathSnapshot(parent, repoID, format)
		if err != nil {
			return nil, err
		}
		shelf := filepath.Join(parent, format)
		if looksLikeModelDir(candidate) {
			checks = append(checks, Check{Location: "shelf", Root: shelf, Result: "hit"})
			path := candidate
			return &ResolveResult{Status: "found", Source: "local_shelf", Format: format, Path: &path, Checks: checks}, nil
		}
		checks = append(checks, Check{Location: "shelf", Root: shelf, Result: "miss"})
	}

	if !cfg.AllowDownloads {
		return &ResolveResult{Status: "missing", Source: "none", Format: format, Path: nil, Checks: checks}, nil
	}

	// Download snapshot into primary shelf.
	finalPath, err := ShelfPathSnapshot(cfg.ShelfRoot, repoID, format)
	if err != nil {
		return nil, err
	}
	dirExisted := dirExists(finalPath)
	if err := os.MkdirAll(finalPath, 0o755); err != nil {
		return nil, err
	}
	if err := downloadSnapshot(repoID, finalPath, format); err != nil {
		cleanupOnFailure(finalPath, dirExisted)
		return nil, fmt.Errorf("snapshot download failed: %w", err)
	}

	return &ResolveResult{Status: "downloaded", Source: "huggingface", Format: format, Path: &finalPath, Checks: checks}, nil
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// cleanupOnFailure removes a directory tree that was created for a download
// that subsequently failed. If the directory existed before the download
// attempt, it is left untouched.
func cleanupOnFailure(dir string, existedBefore bool) {
	if existedBefore {
		return
	}
	_ = os.RemoveAll(dir)
}

// hfRepoFile represents a file entry from the HF API.
type hfRepoFile struct {
	Filename string `json:"rfilename"`
}

func downloadSnapshot(repoID, destDir, format string) error {
	// List files in the repo via HF API.
	apiURL := fmt.Sprintf("https://huggingface.co/api/models/%s", repoID)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return err
	}
	if token := os.Getenv("HF_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if token := os.Getenv("HUGGING_FACE_HUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HF API returned %d for %s", resp.StatusCode, repoID)
	}

	var repoInfo struct {
		Siblings []hfRepoFile `json:"siblings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&repoInfo); err != nil {
		return err
	}

	for _, f := range repoInfo.Siblings {
		if !matchesAllowPatterns(f.Filename, format) {
			continue
		}
		url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repoID, f.Filename)
		dest := filepath.Join(destDir, f.Filename)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := downloadFile(url, dest); err != nil {
			return fmt.Errorf("downloading %s: %w", f.Filename, err)
		}
	}
	return nil
}

func matchesAllowPatterns(filename, format string) bool {
	if format != "safetensors" {
		// MLX: download everything.
		return true
	}
	base := filepath.Base(filename)
	for _, pattern := range SafetensorsAllowPatterns {
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
	}
	return false
}

func downloadFile(url, dest string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	// Support HF token from environment.
	if token := os.Getenv("HF_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if token := os.Getenv("HUGGING_FACE_HUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return clarify401(url)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// clarify401 distinguishes "repo not found" from "repo requires authentication"
// by probing the HF models API endpoint for the repo.
func clarify401(fileURL string) error {
	// Extract repo ID from URL like https://huggingface.co/<owner>/<repo>/resolve/main/<file>
	repoID := extractRepoID(fileURL)
	if repoID == "" {
		return fmt.Errorf("HTTP 401 for %s", fileURL)
	}

	// HEAD the models API — HF returns 404 for truly nonexistent repos,
	// 200 or 401/403 for repos that exist but require auth.
	checkURL := fmt.Sprintf("https://huggingface.co/api/models/%s", repoID)
	headReq, err := http.NewRequest("HEAD", checkURL, nil)
	if err != nil {
		return fmt.Errorf("HTTP 401 for %s", fileURL)
	}
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		return fmt.Errorf("HTTP 401 for %s", fileURL)
	}
	headResp.Body.Close()

	switch headResp.StatusCode {
	case 404:
		return fmt.Errorf("repository %q not found on Hugging Face", repoID)
	case 200, 401, 403:
		return fmt.Errorf("repository %q requires authentication — set HF_TOKEN", repoID)
	default:
		// Unexpected status (429, 5xx, etc.) — fall back to original context.
		return fmt.Errorf("HTTP 401 for %s (probe returned %d)", fileURL, headResp.StatusCode)
	}
}

// extractRepoID extracts "owner/repo" from a HuggingFace file URL.
func extractRepoID(url string) string {
	// URL format: https://huggingface.co/<owner>/<repo>/resolve/main/<file>
	const prefix = "https://huggingface.co/"
	if !strings.HasPrefix(url, prefix) {
		return ""
	}
	path := strings.TrimPrefix(url, prefix)
	parts := strings.SplitN(path, "/", 4)
	if len(parts) < 3 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}
