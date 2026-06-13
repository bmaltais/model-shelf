// Package hf provides a Hugging Face HTTP client with auth, resume, and progress.
package hf

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/schollz/progressbar/v3"
)

const defaultHFBase = "https://huggingface.co"

// DownloadOptions configures a DownloadFile call.
type DownloadOptions struct {
	Token      string // if empty, LoadToken() is used
	NoProgress bool   // suppress progress bar (tests/JSON mode)
}

// SnapshotOptions configures a DownloadSnapshot call.
type SnapshotOptions struct {
	AllowPatterns []string // nil means download everything
	Token         string
	NoProgress    bool
	Workers       int // parallel download workers; 0 = 4
}

// LoadToken returns the HF auth token, or "" if none is configured.
// Priority: HF_TOKEN env → ~/.cache/huggingface/token file.
func LoadToken() string {
	if tok := os.Getenv("HF_TOKEN"); tok != "" {
		return tok
	}
	home, _ := os.UserHomeDir()
	tokenPath := filepath.Join(home, ".cache", "huggingface", "token")
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// DownloadFile downloads url to dest, resuming from a .partial file if present.
// On success, renames .partial → dest. On error, deletes .partial.
func DownloadFile(url, dest string, opts DownloadOptions) error {
	token := opts.Token
	if token == "" {
		token = LoadToken()
	}

	partialPath := dest + ".partial"
	var offset int64
	if info, err := os.Stat(partialPath); err == nil {
		offset = info.Size()
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		os.Remove(partialPath)
		return fmt.Errorf("GET %s: status %s", url, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	flags := os.O_CREATE | os.O_WRONLY
	if resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		offset = 0
	}

	f, err := os.OpenFile(partialPath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("opening partial file: %w", err)
	}

	var writer io.Writer = f
	if !opts.NoProgress {
		total := resp.ContentLength
		if offset > 0 && total > 0 {
			total += offset
		}
		bar := progressbar.DefaultBytes(total, filepath.Base(dest))
		if offset > 0 {
			bar.Add64(offset)
		}
		writer = io.MultiWriter(f, bar)
	}

	buf := make([]byte, 2*1024*1024) // 2 MB copy buffer — reduces syscalls on large files
	if _, err := io.CopyBuffer(writer, resp.Body, buf); err != nil {
		f.Close()
		os.Remove(partialPath)
		return fmt.Errorf("writing %s: %w", dest, err)
	}
	f.Close()

	if err := os.Rename(partialPath, dest); err != nil {
		os.Remove(partialPath)
		return fmt.Errorf("renaming partial file: %w", err)
	}
	return nil
}

type hfFile struct {
	RfileName string `json:"rfilename"`
}

// DownloadSnapshot downloads all files in a HF repo snapshot to destDir.
// Files are downloaded in parallel. allowPatterns uses simple glob matching.
func DownloadSnapshot(repoID, destDir string, opts SnapshotOptions) error {
	token := opts.Token
	if token == "" {
		token = LoadToken()
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	files, err := listRepoFiles(repoID, token)
	if err != nil {
		return err
	}

	var toDownload []string
	for _, f := range files {
		if matchesPatterns(f, opts.AllowPatterns) {
			toDownload = append(toDownload, f)
		}
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	type job struct{ filename string }
	jobs := make(chan job, len(toDownload))
	for _, f := range toDownload {
		jobs <- job{f}
	}
	close(jobs)

	var wg sync.WaitGroup
	errs := make(chan error, len(toDownload))

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				url := fmt.Sprintf("%s/%s/resolve/main/%s", defaultHFBase, repoID, j.filename)
				dest := filepath.Join(destDir, filepath.FromSlash(j.filename))
				if err := DownloadFile(url, dest, DownloadOptions{Token: token, NoProgress: opts.NoProgress}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		return err // return first error
	}
	return nil
}

func listRepoFiles(repoID, token string) ([]string, error) {
	url := fmt.Sprintf("%s/api/models/%s", defaultHFBase, repoID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching repo info for %s: %w", repoID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("repo info for %s: status %s", repoID, resp.Status)
	}
	var info struct {
		Siblings []hfFile `json:"siblings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding repo info: %w", err)
	}
	var names []string
	for _, s := range info.Siblings {
		names = append(names, s.RfileName)
	}
	return names, nil
}

func matchesPatterns(filename string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	base := filepath.Base(filename)
	for _, p := range patterns {
		if matched, _ := filepath.Match(p, base); matched {
			return true
		}
		if matched, _ := filepath.Match(p, filename); matched {
			return true
		}
	}
	return false
}
