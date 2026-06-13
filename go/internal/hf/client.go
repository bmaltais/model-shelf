// Package hf provides a Hugging Face HTTP client with auth, resume, and progress.
package hf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"
)

const (
	defaultHFBase   = "https://huggingface.co"
	downloadChunk   = 10 * 1024 * 1024 // 10 MB — matches huggingface_hub Python library
	maxRetries      = 5
	baseRetryWait   = time.Second
	maxRetryWait    = 8 * time.Second
	metaTimeout     = 30 * time.Second
)

var reSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// hfTransport strips Authorization on cross-domain redirects (HF → CDN).
type hfTransport struct{ base http.RoundTripper }

func (t *hfTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(req)
}

// hfClient is a shared HTTP client tuned for HF Hub downloads.
var hfClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
	// Strip Authorization when a redirect crosses to a different host (e.g. HF → S3/CDN).
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 {
			prev := via[len(via)-1]
			if req.URL.Host != prev.URL.Host {
				req.Header.Del("Authorization")
			}
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

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
	data, err := os.ReadFile(filepath.Join(home, ".cache", "huggingface", "token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// DownloadFile downloads url to dest with resume, retry, and optional progress.
// Temp file is dest+".partial". On success it is renamed to dest atomically.
func DownloadFile(fileURL, dest string, opts DownloadOptions) error {
	token := opts.Token
	if token == "" {
		token = LoadToken()
	}

	// Head request to get content length for disk space check and progress total.
	totalSize, etag := fetchMeta(fileURL, token)

	// Disk space preflight.
	if totalSize > 0 {
		if stat, err := os.Stat(filepath.Dir(dest)); err == nil && stat.IsDir() {
			if free := diskFree(filepath.Dir(dest)); free > 0 && free < uint64(totalSize) {
				return fmt.Errorf("not enough disk space: need %d bytes, have %d", totalSize, free)
			}
		}
	}

	partialPath := dest + ".partial"

	var bar *progressbar.ProgressBar
	if !opts.NoProgress {
		bar = progressbar.DefaultBytes(totalSize, filepath.Base(dest))
	}

	return downloadWithRetry(fileURL, dest, partialPath, token, totalSize, etag, bar)
}

func downloadWithRetry(fileURL, dest, partialPath, token string, totalSize int64, etag string, bar *progressbar.ProgressBar) error {
	wait := baseRetryWait
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := downloadOnce(fileURL, dest, partialPath, token, totalSize, etag, bar)
		if err == nil {
			return nil
		}
		if !isRetryable(err) || attempt == maxRetries {
			os.Remove(partialPath)
			return err
		}
		time.Sleep(wait)
		wait = time.Duration(math.Min(float64(wait*2), float64(maxRetryWait)))
	}
	return fmt.Errorf("download failed after %d retries", maxRetries)
}

func downloadOnce(fileURL, dest, partialPath, token string, totalSize int64, etag string, bar *progressbar.ProgressBar) error {
	// Resume from existing partial file.
	var offset int64
	if info, err := os.Stat(partialPath); err == nil {
		offset = info.Size()
	}

	req, err := http.NewRequest(http.MethodGet, fileURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	setCommonHeaders(req, token)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := hfClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored our Range header — start fresh.
		offset = 0
	case http.StatusPartialContent:
		// Resume from offset — good.
	case http.StatusTooManyRequests:
		wait := retryAfter(resp)
		time.Sleep(wait)
		return fmt.Errorf("rate limited (429)")
	default:
		return fmt.Errorf("GET %s: status %s", fileURL, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	flags := os.O_CREATE | os.O_WRONLY
	if resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(partialPath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("opening partial file: %w", err)
	}

	if bar != nil && offset > 0 {
		bar.Add64(offset)
	}

	var w io.Writer = f
	if bar != nil {
		w = io.MultiWriter(f, bar)
	}

	buf := make([]byte, downloadChunk)
	if _, err := io.CopyBuffer(w, resp.Body, buf); err != nil {
		f.Close()
		return err // retryable — partial file kept for next attempt
	}
	f.Close()

	// SHA256 integrity check when ETag is a blob hash (LFS files).
	if etag != "" && reSHA256.MatchString(etag) {
		if err := verifySHA256(partialPath, etag); err != nil {
			os.Remove(partialPath)
			return fmt.Errorf("integrity check failed: %w", err)
		}
	}

	if err := os.Rename(partialPath, dest); err != nil {
		os.Remove(partialPath)
		return fmt.Errorf("renaming partial file: %w", err)
	}
	return nil
}

type hfFile struct {
	RfileName string `json:"rfilename"`
}

// DownloadSnapshot downloads all files in a HF repo snapshot to destDir in parallel.
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
				u := fmt.Sprintf("%s/%s/resolve/main/%s", defaultHFBase, repoID, j.filename)
				dst := filepath.Join(destDir, filepath.FromSlash(j.filename))
				if err := DownloadFile(u, dst, DownloadOptions{Token: token, NoProgress: opts.NoProgress}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		return err
	}
	return nil
}

// fetchMeta does a HEAD request to get content-length and etag without downloading.
func fetchMeta(fileURL, token string) (size int64, etag string) {
	req, err := http.NewRequest(http.MethodHead, fileURL, nil)
	if err != nil {
		return -1, ""
	}
	setCommonHeaders(req, token)
	client := &http.Client{Timeout: metaTimeout, CheckRedirect: hfClient.CheckRedirect}
	resp, err := client.Do(req)
	if err != nil {
		return -1, ""
	}
	resp.Body.Close()
	etag = strings.Trim(resp.Header.Get("ETag"), `"`)
	return resp.ContentLength, etag
}

func listRepoFiles(repoID, token string) ([]string, error) {
	u := fmt.Sprintf("%s/api/models/%s", defaultHFBase, repoID)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	setCommonHeaders(req, token)
	client := &http.Client{Timeout: metaTimeout, CheckRedirect: hfClient.CheckRedirect}
	resp, err := client.Do(req)
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

func setCommonHeaders(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// Prevent content-encoding compression so Content-Length is accurate for
	// progress bars and disk space checks (mirrors huggingface_hub behaviour).
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "model-shelf/0.7 (go)")
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

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "rate limited") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "status 500") ||
		strings.Contains(s, "status 502") ||
		strings.Contains(s, "status 503") ||
		strings.Contains(s, "status 504")
}

func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs+1) * time.Second
		}
	}
	return baseRetryWait
}

func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, downloadChunk)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, want)
	}
	return nil
}

func diskFree(dir string) uint64 {
	return diskFreeOS(dir)
}

// resolveURL is used in tests to override the base URL.
func resolveURL(base, repoID, filename string) string {
	u, _ := url.JoinPath(base, repoID, "resolve", "main", filename)
	return u
}
