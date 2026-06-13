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
	readBufSize     = 10 * 1024 * 1024 // 10 MB read buffer — matches huggingface_hub
	parallelWorkers = 8                 // concurrent range connections per file
	parallelMin     = 50 * 1024 * 1024 // only parallelize when size is known and > 50 MB
	maxRetries      = 5
	baseRetryWait   = time.Second
	maxRetryWait    = 8 * time.Second
	metaTimeout     = 30 * time.Second
)

var reSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// hfClient is a shared HTTP client tuned for HF Hub downloads.
var hfClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: parallelWorkers + 4,
		IdleConnTimeout:     90 * time.Second,
	},
	// Strip Authorization when a redirect crosses to a different host (HF → S3/CDN).
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && req.URL.Host != via[len(via)-1].URL.Host {
			req.Header.Del("Authorization")
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
	Workers       int // parallel files for snapshot; 0 = 4
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

// DownloadFile downloads fileURL to dest. For large files (>50 MB) it uses
// parallel range requests. On success the file is atomically renamed to dest.
func DownloadFile(fileURL, dest string, opts DownloadOptions) error {
	token := opts.Token
	if token == "" {
		token = LoadToken()
	}

	totalSize, etag := fetchMeta(fileURL, token)

	if totalSize > 0 {
		if free := diskFree(filepath.Dir(dest)); free > 0 && free < uint64(totalSize) {
			return fmt.Errorf("not enough disk space: need %s, have %s",
				fmtBytes(totalSize), fmtBytes(int64(free)))
		}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	partialPath := dest + ".partial"

	var bar *progressbar.ProgressBar
	if !opts.NoProgress {
		bar = progressbar.DefaultBytes(totalSize, filepath.Base(dest))
	}

	var err error
	if totalSize >= parallelMin {
		err = downloadParallel(fileURL, partialPath, token, totalSize, bar)
		if err != nil && strings.Contains(err.Error(), "range not supported") {
			os.Remove(partialPath)
			err = downloadSingle(fileURL, partialPath, token, totalSize, bar)
		}
	} else {
		err = downloadSingle(fileURL, partialPath, token, totalSize, bar)
	}
	if err != nil {
		os.Remove(partialPath)
		return err
	}

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

// downloadParallel splits the file into parallelWorkers equal ranges and
// downloads them concurrently into a pre-allocated file using WriteAt.
func downloadParallel(fileURL, partialPath, token string, totalSize int64, bar *progressbar.ProgressBar) error {
	// Pre-allocate the file so all goroutines can WriteAt independently.
	f, err := os.OpenFile(partialPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := f.Truncate(totalSize); err != nil {
		f.Close()
		return fmt.Errorf("pre-allocating %s: %w", partialPath, err)
	}
	f.Close()

	type chunkRange struct{ start, end int64 }
	chunkSize := (totalSize + parallelWorkers - 1) / parallelWorkers

	chunks := make(chan chunkRange, parallelWorkers)
	for start := int64(0); start < totalSize; start += chunkSize {
		end := start + chunkSize - 1
		if end >= totalSize {
			end = totalSize - 1
		}
		chunks <- chunkRange{start, end}
	}
	close(chunks)

	errs := make(chan error, parallelWorkers)
	var wg sync.WaitGroup

	for i := 0; i < parallelWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine opens its own file descriptor for WriteAt.
			f, err := os.OpenFile(partialPath, os.O_WRONLY, 0o644)
			if err != nil {
				errs <- err
				return
			}
			defer f.Close()

			for c := range chunks {
				if err := downloadChunk(fileURL, token, c.start, c.end, f, bar); err != nil {
					errs <- fmt.Errorf("chunk %d-%d: %w", c.start, c.end, err)
					return
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

// downloadChunk downloads bytes [start, end] of fileURL and writes them at
// the correct offset in f using WriteAt (safe for concurrent goroutines).
func downloadChunk(fileURL, token string, start, end int64, f *os.File, bar *progressbar.ProgressBar) error {
	req, err := http.NewRequest(http.MethodGet, fileURL, nil)
	if err != nil {
		return err
	}
	setCommonHeaders(req, token)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := hfClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("server returned %s (range not supported?)", resp.Status)
	}

	buf := make([]byte, readBufSize)
	offset := start
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.WriteAt(buf[:n], offset); writeErr != nil {
				return writeErr
			}
			offset += int64(n)
			if bar != nil {
				bar.Add(n)
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

// downloadSingle downloads the file as a single stream with resume support.
func downloadSingle(fileURL, partialPath, token string, totalSize int64, bar *progressbar.ProgressBar) error {
	wait := baseRetryWait
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := downloadSingleOnce(fileURL, partialPath, token, totalSize, bar)
		if err == nil {
			return nil
		}
		if !isRetryable(err) || attempt == maxRetries {
			return err
		}
		time.Sleep(wait)
		wait = time.Duration(math.Min(float64(wait*2), float64(maxRetryWait)))
	}
	return fmt.Errorf("download failed after %d retries", maxRetries)
}

func downloadSingleOnce(fileURL, partialPath, token string, totalSize int64, bar *progressbar.ProgressBar) error {
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
		offset = 0
	case http.StatusPartialContent:
		// resume — good
	case http.StatusTooManyRequests:
		time.Sleep(retryAfter(resp))
		return fmt.Errorf("rate limited (429)")
	default:
		return fmt.Errorf("GET %s: status %s", fileURL, resp.Status)
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

	buf := make([]byte, readBufSize)
	if _, err := io.CopyBuffer(w, resp.Body, buf); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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

// fetchMeta does a HEAD request to get content-length and etag.
// For LFS files HF returns a 302 whose X-Linked-ETag header carries the
// SHA-256 of the actual content; the CDN ETag is unrelated to content SHA-256.
// We therefore stop at the first response to read X-Linked-ETag, then follow
// the redirect (if any) to obtain the real Content-Length.
func fetchMeta(fileURL, token string) (size int64, etag string) {
	req, err := http.NewRequest(http.MethodHead, fileURL, nil)
	if err != nil {
		return -1, ""
	}
	setCommonHeaders(req, token)

	noRedirect := &http.Client{
		Timeout: metaTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return -1, ""
	}
	resp.Body.Close()

	// X-Linked-ETag is the content SHA-256 for LFS files; fall back to ETag.
	if e := strings.Trim(resp.Header.Get("X-Linked-ETag"), `"`); e != "" {
		etag = e
	} else {
		etag = strings.Trim(resp.Header.Get("ETag"), `"`)
	}

	if resp.StatusCode == http.StatusOK {
		return resp.ContentLength, etag
	}

	// Follow the redirect to get Content-Length from the final storage URL.
	location := resp.Header.Get("Location")
	if location == "" {
		return -1, etag
	}
	req2, err := http.NewRequest(http.MethodHead, location, nil)
	if err != nil {
		return -1, etag
	}
	req2.Header.Set("Accept-Encoding", "identity")
	followClient := &http.Client{Timeout: metaTimeout, CheckRedirect: hfClient.CheckRedirect}
	if resp2, err := followClient.Do(req2); err == nil {
		resp2.Body.Close()
		return resp2.ContentLength, etag
	}
	return -1, etag
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
	// Prevents gzip compression so Content-Length reflects the true file size.
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
	buf := make([]byte, readBufSize)
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

func fmtBytes(n int64) string {
	f := float64(n)
	for _, unit := range []string{"B", "KB", "MB", "GB", "TB"} {
		if f < 1024 || unit == "TB" {
			return fmt.Sprintf("%.1f %s", f, unit)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1f TB", f)
}
