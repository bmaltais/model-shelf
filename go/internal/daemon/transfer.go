package daemon

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexziskind1/model-shelf/internal/resolver"
)

// handleModelDownload serves model files from the local shelf.
// Route: GET /v1/models/{format}/{publisher}/{repo}/download[?quant=Q]
//
// Supported formats:
//   - gguf: serves the single .gguf file
//   - mlx/safetensors: streams a tar archive of the model directory
func (d *Daemon) handleModelDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse path: /v1/models/{format}/{publisher}/{repo}/download
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/models/"), "/")
	if len(pathParts) < 4 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid path: expected /v1/models/{format}/{publisher}/{repo}/download"})
		return
	}

	// Last segment must be "download".
	if pathParts[len(pathParts)-1] != "download" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "path must end with /download"})
		return
	}
	pathParts = pathParts[:len(pathParts)-1]

	format := pathParts[0]
	if format != "gguf" && format != "mlx" && format != "safetensors" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "unsupported format: " + format})
		return
	}

	if len(pathParts) != 3 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid path: expected exactly /v1/models/{format}/{publisher}/{repo}/download"})
		return
	}

	publisher := pathParts[1]
	repo := pathParts[2]
	repoID := publisher + "/" + repo

	// Determine quant for GGUF.
	quant := r.URL.Query().Get("quant")
	if format == "gguf" && quant == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "quant query parameter is required for gguf format"})
		return
	}

	// Resolve the shelf path.
	shelfPath, err := resolveShelfPath(d.cfg.ShelfRoot, format, repoID, quant)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Check the file/dir exists.
	info, err := os.Stat(shelfPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "model not found on this node"})
		return
	}

	// Update last-accessed on the source node (with inventory lock).
	var size int64
	if info.IsDir() {
		size = dirSizeBytes(shelfPath)
	} else {
		size = info.Size()
	}
	release, lockErr := acquireInventoryLock()
	if lockErr != nil {
		log.Printf("transfer: failed to acquire inventory lock: %v", lockErr)
	} else {
		d.inventory.Touch(repoID, format, quant, size)
		if err := d.inventory.Save(); err != nil {
			log.Printf("transfer: failed to save inventory after touch: %v", err)
		}
		release()
	}

	// Serve the model.
	if format == "gguf" {
		// GGUF is a single file. Serve with a large copy buffer instead of
		// http.ServeFile (which uses a 32KB pool buffer) to reduce syscall
		// overhead ~32x on fast LANs. Also supports Range requests for resume.
		f, openErr := os.Open(shelfPath)
		if openErr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to open model file"})
			return
		}
		defer f.Close()

		// Support Range: bytes=N- for transfer resume.
		startByte := int64(0)
		if rangeHdr := r.Header.Get("Range"); rangeHdr != "" {
			var n int64
			if _, scanErr := fmt.Sscanf(rangeHdr, "bytes=%d-", &n); scanErr == nil && n > 0 && n < info.Size() {
				if _, seekErr := f.Seek(n, io.SeekStart); seekErr == nil {
					startByte = n
				}
			}
		}

		remaining := info.Size() - startByte
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(shelfPath)))
		if startByte > 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", startByte, info.Size()-1, info.Size()))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", remaining))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", remaining))
		}
		buf := make([]byte, transferBufferSize)
		if _, err := io.CopyBuffer(w, f, buf); err != nil {
			log.Printf("transfer: gguf stream error for %s: %v", shelfPath, err)
		}
	} else {
		// Snapshot format (mlx/safetensors) — stream as tar archive.
		if !info.IsDir() || !looksLikeModelDir(shelfPath) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "model directory not found or incomplete"})
			return
		}
		// Pre-calculate the exact archive size so the receiver knows bytes_total
		// and the response uses a fixed-length body instead of chunked encoding.
		// Chunked encoding without explicit flushing can stall mid-stream on the
		// trailing chunk marker when large files are transferred.
		if tarSize, err := tarredSize(shelfPath); err == nil {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", tarSize))
		} else {
			log.Printf("transfer: failed to calculate tar size for %s: %v", shelfPath, err)
		}
		w.Header().Set("Content-Type", "application/x-tar")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-%s.tar", publisher, repo))
		if err := streamTarDirectory(w, shelfPath); err != nil {
			log.Printf("transfer: tar stream error for %s: %v", shelfPath, err)
		}
	}
}

// resolveShelfPath returns the shelf path for a model.
func resolveShelfPath(shelfRoot, format, repoID, quant string) (string, error) {
	if format == "gguf" {
		return resolver.ShelfPathGGUF(shelfRoot, repoID, quant)
	}
	return resolver.ShelfPathSnapshot(shelfRoot, repoID, format)
}

// streamTarDirectory streams a directory as a tar archive to w.
// Only regular files are included; symlinks and other special files are skipped.
// Returns the first error encountered during walking or writing.
func streamTarDirectory(w io.Writer, dir string) error {
	tw := tar.NewWriter(w)
	buf := make([]byte, transferBufferSize)

	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyBuffer(tw, f, buf)
		f.Close()
		return copyErr
	}); err != nil {
		tw.Close()
		return err
	}
	return tw.Close()
}

// tarredSize calculates the exact byte count of a tar archive of dir.
// Each regular file contributes a 512-byte header block plus the file's data
// rounded up to the next 512-byte boundary. The archive ends with 1024 zero bytes.
func tarredSize(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		total += 512                        // tar header block
		total += (info.Size() + 511) &^ 511 // file data padded to 512-byte boundary
		return nil
	})
	if err != nil {
		return 0, err
	}
	total += 1024 // two 512-byte end-of-archive zero blocks
	return total, nil
}

const (
	// progressReportInterval is how often progress is reported to the job store (1MB).
	progressReportInterval = 1024 * 1024

	// transferBufferSize is the buffer size for peer-to-peer transfers (1MB).
	// The default io.Copy buffer (32KB) causes excessive syscall overhead on
	// fast LANs, making the first transfer ~60x slower than subsequent ones
	// due to TCP slow-start amplification.
	transferBufferSize = 1024 * 1024

	// transferSocketBufSize is the OS-level TCP socket buffer (SO_RCVBUF/SO_SNDBUF) for
	// peer transfers (4MB). The default kernel receive window (~87KB on Linux) limits
	// throughput on high-latency links: at 40ms RTT, 87KB window → ~2 MB/s ceiling.
	// A 4MB window raises that ceiling to ~100 MB/s, matching Tailscale's capacity.
	transferSocketBufSize = 4 * 1024 * 1024

	// resumeThresholdBytes is the minimum .partial file size to attempt resume-from-offset.
	// Partial files smaller than this are discarded and the transfer starts fresh.
	resumeThresholdBytes = 100 * 1024 * 1024 // 100MB
)

// peerSource describes a mesh peer that has a model available for transfer.
type peerSource struct {
	Name    string
	Address string
	Port    int
}

// findPeerWithModel queries mesh peers' inventories to find one that has the model.
func (d *Daemon) findPeerWithModel(repoID, format, quant string) *peerSource {
	nodes := d.gossip.Nodes()
	meshKey := d.cfg.MeshKey

	for _, node := range nodes {
		if node.Name == d.cfg.Name || node.Status == StatusOffline {
			continue
		}

		hostPort := net.JoinHostPort(node.Address, fmt.Sprintf("%d", node.Port))
		url := fmt.Sprintf("http://%s/v1/inventory", hostPort)
		client := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		if meshKey != "" {
			req.Header.Set("Authorization", "Bearer "+meshKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		var entries []InventoryEntry
		if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		// Check if this peer has the model.
		target := &InventoryEntry{RepoID: repoID, Format: format, Quant: strings.ToUpper(quant)}
		targetKey := target.Key()
		for _, e := range entries {
			if e.Key() == targetKey {
				return &peerSource{
					Name:    node.Name,
					Address: node.Address,
					Port:    node.Port,
				}
			}
		}
	}
	return nil
}

// transferFromPeer downloads a model from a peer node's download endpoint.
// Returns nil on success, or an error if the transfer fails.
func (d *Daemon) transferFromPeer(ctx context.Context, jobID string, peer *peerSource, repoID, format, quant string) error {
	d.jobs.SetTransferring(jobID, peer.Name)
	log.Printf("transfer: starting peer transfer of %s (format=%s, quant=%s) from %s, job=%s",
		repoID, format, quant, peer.Name, jobID)

	publisher, repo, err := splitRepoIDParts(repoID)
	if err != nil {
		return err
	}

	// Build the download URL.
	hostPort := net.JoinHostPort(peer.Address, fmt.Sprintf("%d", peer.Port))
	url := fmt.Sprintf("http://%s/v1/models/%s/%s/%s/download", hostPort, format, publisher, repo)
	if format == "gguf" && quant != "" {
		url += "?quant=" + quant
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	if d.cfg.MeshKey != "" {
		req.Header.Set("Authorization", "Bearer "+d.cfg.MeshKey)
	}

	// For GGUF, check for a large partial file and request resume from offset.
	var startOffset int64
	if format == "gguf" {
		startOffset = ggufPartialOffset(d.cfg.ShelfRoot, repoID, quant)
		if startOffset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startOffset))
			log.Printf("transfer: resuming %s from offset %d", repoID, startOffset)
		}
	}

	// Use a transport with large userspace I/O buffers and a custom dialer that
	// raises the OS TCP socket window (SO_RCVBUF/SO_SNDBUF) to 4MB. The default
	// kernel receive window (~87KB) is the primary throughput ceiling on
	// high-latency links such as Tailscale: 87KB ÷ 40ms RTT ≈ 2 MB/s.
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			if tc, ok := conn.(*net.TCPConn); ok {
				if err := tc.SetReadBuffer(transferSocketBufSize); err != nil {
					log.Printf("transfer: SetReadBuffer failed (degraded throughput): %v", err)
				}
				if err := tc.SetWriteBuffer(transferSocketBufSize); err != nil {
					log.Printf("transfer: SetWriteBuffer failed (degraded throughput): %v", err)
				}
			}
			return conn, nil
		},
		ReadBufferSize:     transferBufferSize,
		WriteBufferSize:    transferBufferSize,
		DisableCompression: true,
	}
	client := &http.Client{Timeout: 0, Transport: transport} // No timeout for large transfers.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to peer %s: %w", peer.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("peer %s returned HTTP %d", peer.Name, resp.StatusCode)
	}

	// Set total bytes from Content-Length if available.
	// For a 206 response, Content-Length is the remaining bytes; add startOffset for total.
	if resp.ContentLength > 0 {
		total := resp.ContentLength + startOffset
		d.jobs.SetProgress(jobID, startOffset, total)
	}

	if format == "gguf" {
		return d.transferGGUF(ctx, jobID, resp.Body, resp.ContentLength, repoID, quant, startOffset)
	}
	return d.transferSnapshot(ctx, jobID, resp.Body, resp.ContentLength, repoID, format)
}

// ggufPartialOffset returns the byte offset to resume a GGUF transfer from, based
// on the size of any existing .partial file. Returns 0 if no large partial exists.
func ggufPartialOffset(shelfRoot, repoID, quant string) int64 {
	path, err := resolver.ShelfPathGGUF(shelfRoot, repoID, quant)
	if err != nil {
		return 0
	}
	info, err := os.Stat(path + resolver.PartialSuffix)
	if err != nil || info.Size() < resumeThresholdBytes {
		return 0
	}
	return info.Size()
}

// transferGGUF downloads a single GGUF file from the response body.
// startOffset > 0 means the response is a 206 partial; the partial file already
// contains startOffset bytes and the file is opened in append mode.
func (d *Daemon) transferGGUF(ctx context.Context, jobID string, body io.Reader, responseBytes int64, repoID, quant string, startOffset int64) error {
	destPath, err := resolver.ShelfPathGGUF(d.cfg.ShelfRoot, repoID, quant)
	if err != nil {
		return err
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	// Write to a partial file, then rename atomically.
	partial := destPath + resolver.PartialSuffix

	var f *os.File
	if startOffset > 0 {
		// Resuming: open existing partial in append mode.
		f, err = os.OpenFile(partial, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			// Partial disappeared (TOCTOU after we sent "Range: bytes=N-") or is
			// unwritable. The response body contains only the tail bytes. Do not
			// Create + write the short body (would yield truncated/corrupt final
			// model after rename). Return err so caller falls back to full HF.
			return fmt.Errorf("failed to open %s for resume at offset %d: %w", partial, startOffset, err)
		}
	} else {
		f, err = os.Create(partial)
	}
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}

	// totalBytes is the full expected file size (already-downloaded + remaining).
	totalBytes := responseBytes + startOffset

	// Use progressReader to throttle progress updates (every 1MB).
	pr := &progressReader{reader: body, jobID: jobID, total: totalBytes, jobs: d.jobs, read: startOffset, lastRpt: startOffset}
	buf := make([]byte, transferBufferSize)
	_, copyErr := io.CopyBuffer(f, pr, buf)
	if copyErr != nil {
		f.Close()
		if ctx.Err() == nil {
			// Non-cancellation error: discard partial (corrupted/incomplete).
			os.Remove(partial)
		}
		// On cancellation, leave the partial file for potential resume next run.
		return fmt.Errorf("writing file: %w", copyErr)
	}

	if err := f.Close(); err != nil {
		os.Remove(partial)
		return fmt.Errorf("closing file: %w", err)
	}

	// Atomic rename.
	if err := os.Rename(partial, destPath); err != nil {
		os.Remove(partial)
		return fmt.Errorf("finalizing file: %w", err)
	}

	return nil
}

// transferSnapshot downloads a tar archive from the response body and extracts it.
// Uses a dot-prefixed staging directory to prevent incomplete models from appearing
// in inventory scans. Only renames to the final path on successful completion.
// On context cancellation, the staging directory is cleaned up.
func (d *Daemon) transferSnapshot(ctx context.Context, jobID string, body io.Reader, totalBytes int64, repoID, format string) error {
	destDir, err := resolver.ShelfPathSnapshot(d.cfg.ShelfRoot, repoID, format)
	if err != nil {
		return err
	}

	// Use a dot-prefixed staging directory (ScanShelf skips dot-prefixed dirs).
	stagingDir := filepath.Join(filepath.Dir(destDir), "."+filepath.Base(destDir)+".transferring")

	// Clean up any prior failed staging attempt.
	os.RemoveAll(stagingDir)

	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return fmt.Errorf("creating staging directory: %w", err)
	}

	// Track bytes read for progress.
	pr := &progressReader{reader: body, jobID: jobID, total: totalBytes, jobs: d.jobs}
	tr := tar.NewReader(pr)
	buf := make([]byte, transferBufferSize) // Reuse buffer across all files in the archive.

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Clean up on failure.
			os.RemoveAll(stagingDir)
			return fmt.Errorf("reading tar: %w", err)
		}

		targetPath := filepath.Join(stagingDir, header.Name)

		// Security: prevent path traversal using filepath.Rel.
		rel, relErr := filepath.Rel(stagingDir, filepath.Clean(targetPath))
		if relErr != nil || strings.HasPrefix(rel, "..") {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				os.RemoveAll(stagingDir)
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				os.RemoveAll(stagingDir)
				return err
			}
			f, err := os.Create(targetPath)
			if err != nil {
				os.RemoveAll(stagingDir)
				return err
			}
			if _, err := io.CopyBuffer(f, tr, buf); err != nil {
				f.Close()
				os.RemoveAll(stagingDir)
				return fmt.Errorf("extracting %s: %w", header.Name, err)
			}
			f.Close()
		}
	}

	// Remove any existing destination (stale/incomplete from prior attempts).
	os.RemoveAll(destDir)

	// Atomic rename from staging to final path.
	if err := os.Rename(stagingDir, destDir); err != nil {
		os.RemoveAll(stagingDir)
		return fmt.Errorf("finalizing snapshot transfer: %w", err)
	}

	return nil
}

// progressReader wraps an io.Reader and reports progress to the job store.
type progressReader struct {
	reader  io.Reader
	jobID   string
	total   int64
	read    int64
	jobs    *JobStore
	lastRpt int64 // last reported byte count (to avoid excessive updates)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.read += int64(n)
	// Report progress every 1MB to avoid lock contention.
	if pr.read-pr.lastRpt >= progressReportInterval {
		pr.jobs.SetProgress(pr.jobID, pr.read, pr.total)
		pr.lastRpt = pr.read
	}
	return n, err
}

// splitRepoIDParts splits "publisher/repo" into its two parts.
func splitRepoIDParts(repoID string) (string, string, error) {
	parts := strings.Split(repoID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo_id must be in 'publisher/repo' format, got: %q", repoID)
	}
	return parts[0], parts[1], nil
}

// updateInventoryAfterTransfer updates the local inventory after a successful peer transfer.
func (d *Daemon) updateInventoryAfterTransfer(jobID, repoID, format, quant string) {
	var destPath string
	var err error
	if format == "gguf" {
		destPath, err = resolver.ShelfPathGGUF(d.cfg.ShelfRoot, repoID, quant)
	} else {
		destPath, err = resolver.ShelfPathSnapshot(d.cfg.ShelfRoot, repoID, format)
	}
	if err != nil {
		log.Printf("transfer: job %s failed to resolve path for inventory: %v", jobID, err)
		return
	}

	var size int64
	if info, statErr := os.Stat(destPath); statErr == nil {
		if info.IsDir() {
			size = dirSizeBytes(destPath)
		} else {
			size = info.Size()
		}
	}

	release, lockErr := acquireInventoryLock()
	if lockErr != nil {
		log.Printf("transfer: job %s failed to acquire inventory lock: %v", jobID, lockErr)
		return
	}
	defer release()

	d.inventory.Touch(repoID, format, quant, size)
	if err := d.inventory.Save(); err != nil {
		log.Printf("transfer: job %s inventory save error: %v", jobID, err)
	}

	// Mark bytes_downloaded as complete (matching bytes_total from Content-Length).
	// Don't overwrite bytes_total — it was set from the wire size at transfer start
	// and must remain consistent throughout the job lifecycle (#156).
	if j := d.jobs.Get(jobID); j != nil && j.BytesTotal > 0 {
		d.jobs.SetProgress(jobID, j.BytesTotal, j.BytesTotal)
	}
}
