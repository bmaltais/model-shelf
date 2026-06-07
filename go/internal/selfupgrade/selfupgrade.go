// Package selfupgrade implements self-upgrade logic for the model-shelf binary.
// It fetches the latest release from GitHub, verifies the SHA256 checksum,
// and atomically replaces the running binary. It is also reusable as a
// library for the future mesh-upgrade daemon endpoint.
package selfupgrade

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubAPILatest   = "https://api.github.com/repos/bmaltais/model-shelf/releases/latest"
	githubDownloadFmt = "https://github.com/bmaltais/model-shelf/releases/download/v%s/model-shelf-%s-%s%s"
	checksumsFmt      = "https://github.com/bmaltais/model-shelf/releases/download/v%s/checksums.txt"
)

// httpClient is used for all HTTP requests; overridable in tests.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// githubRelease is a partial representation of the GitHub releases API response.
type githubRelease struct {
	TagName string `json:"tag_name"`
}

// FetchLatestVersion queries the GitHub releases API and returns the latest
// version string (without the leading "v").
func FetchLatestVersion() (string, error) {
	req, err := http.NewRequest(http.MethodGet, githubAPILatest, nil)
	if err != nil {
		return "", fmt.Errorf("building releases request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "model-shelf-selfupgrade")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d when fetching latest release", resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("parsing releases response: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("releases API returned empty tag_name")
	}
	return strings.TrimPrefix(rel.TagName, "v"), nil
}

// FetchChecksums downloads checksums.txt for the given version and returns
// a map of filename → expected SHA256 hex digest.
func FetchChecksums(version string) (map[string]string, error) {
	url := fmt.Sprintf(checksumsFmt, version)
	resp, err := httpClient.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("downloading checksums.txt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading checksums.txt: HTTP %d", resp.StatusCode)
	}

	sums := make(map[string]string)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Format: "<sha256>  <filename>" (two spaces, as produced by sha256sum)
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		sums[parts[1]] = parts[0]
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading checksums.txt: %w", err)
	}
	return sums, nil
}

// BinaryName returns the expected release asset filename for the given OS and arch.
func BinaryName(goos, goarch string) string {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("model-shelf-%s-%s%s", goos, goarch, ext)
}

// DownloadBinary downloads the binary for the given version/os/arch into a
// temporary file placed in destDir (same filesystem as the destination binary,
// so that the final os.Rename is atomic). The caller is responsible for
// removing the temp file on error.
func DownloadBinary(version, goos, goarch, destDir string) (string, error) {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	url := fmt.Sprintf(githubDownloadFmt, version, goos, goarch, ext)

	resp, err := httpClient.Get(url) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("downloading binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading binary: HTTP %d for %s", resp.StatusCode, url)
	}

	// Write to a temp file in the same directory as the destination binary
	// so that the final os.Rename is atomic (same filesystem).
	tmp, err := os.CreateTemp(destDir, "model-shelf-upgrade-*.tmp")
	if err != nil {
		return "", fmt.Errorf("creating temp file for download: %w", err)
	}
	tmpPath := tmp.Name()

	// Stream the response body, computing SHA256 on the fly would be a bonus
	// but we keep it simple: write first, verify after (VerifyChecksum re-reads).
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("writing downloaded binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("flushing downloaded binary: %w", err)
	}
	return tmpPath, nil
}

// VerifyChecksum computes the SHA256 of the file at path (streaming, no
// full read into memory) and compares it to expected (hex string).
func VerifyChecksum(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening file for checksum: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("computing SHA256: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expected, got)
	}
	return nil
}

// ReplaceBinary saves binPath to binPath+".bak", then atomically renames
// tmpPath to binPath (preserving the original file permissions).
func ReplaceBinary(tmpPath, binPath string) error {
	// Preserve original permissions.
	info, err := os.Stat(binPath)
	if err != nil {
		return fmt.Errorf("stat existing binary: %w", err)
	}
	perm := info.Mode()

	// Apply the same permissions to the new binary before replacing.
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("chmod new binary: %w", err)
	}

	// Save backup.
	bakPath := binPath + ".bak"
	if err := copyFile(binPath, bakPath, perm); err != nil {
		return fmt.Errorf("saving backup to %s: %w", bakPath, err)
	}

	// Atomic rename — works because tmpPath is in the same directory.
	if err := os.Rename(tmpPath, binPath); err != nil {
		return fmt.Errorf("replacing binary: %w", err)
	}
	return nil
}

// copyFile copies src to dst with the given permissions.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Run is the main entry point for the self-upgrade flow. It orchestrates
// version resolution, download, checksum verification, and binary replacement.
//
//   - targetVersion: empty string means "fetch latest from GitHub"
//   - currentVersion: the version currently running (set via ldflags)
//   - yes: skip the confirmation prompt
//   - force: proceed even if already at the target version
//   - stdout/stderr: callers may pass os.Stdout/os.Stderr (nil falls back to os.Stdout/os.Stderr)
//   - promptReader: used to read confirmation; nil falls back to os.Stdin
//
// The caller is responsible for restarting the service after Run returns nil.
func Run(targetVersion, currentVersion string, yes, force bool, stdout, stderr io.Writer, promptReader io.Reader) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	if promptReader == nil {
		promptReader = os.Stdin
	}

	// 1. Resolve target version.
	if targetVersion == "" {
		fmt.Fprintf(stdout, "Fetching latest release...\n")
		v, err := FetchLatestVersion()
		if err != nil {
			return fmt.Errorf("could not determine latest version: %w", err)
		}
		targetVersion = v
	}

	// 2. Compare to running version.
	current := strings.TrimPrefix(currentVersion, "v")
	target := strings.TrimPrefix(targetVersion, "v")

	if current == target && !force {
		fmt.Fprintf(stdout, "Already running model-shelf %s — nothing to do.\n", current)
		fmt.Fprintf(stdout, "(Use --force to reinstall the same version.)\n")
		return nil
	}

	// 3. Determine platform.
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// 4. Print plan and confirm.
	fmt.Fprintf(stdout, "\n  current version : %s\n", currentOrDev(current))
	fmt.Fprintf(stdout, "  target version  : %s\n", target)
	fmt.Fprintf(stdout, "  platform        : %s/%s\n\n", goos, goarch)

	if !yes {
		fmt.Fprintf(stdout, "Proceed? [y/N] ")
		answer, err := readLine(promptReader)
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintf(stdout, "Upgrade cancelled.\n")
			return nil
		}
	}

	// 5. Locate the running binary.
	binPath, err := executablePath()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}

	// 6. Download checksums.txt.
	fmt.Fprintf(stdout, "Downloading checksums.txt...\n")
	sums, err := FetchChecksums(target)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}

	assetName := BinaryName(goos, goarch)
	expectedSHA, ok := sums[assetName]
	if !ok {
		return fmt.Errorf("checksums.txt has no entry for %q — available: %s",
			assetName, joinKeys(sums))
	}

	// 7. Download the new binary to the same directory as the current binary.
	fmt.Fprintf(stdout, "Downloading %s %s...\n", assetName, target)
	tmpPath, err := DownloadBinary(target, goos, goarch, filepath.Dir(binPath))
	if err != nil {
		return fmt.Errorf("downloading new binary: %w", err)
	}
	// Ensure temp file is removed on any subsequent error.
	cleanup := func() { os.Remove(tmpPath) }

	// 8. Verify checksum — abort without touching existing binary on mismatch.
	fmt.Fprintf(stdout, "Verifying SHA256...\n")
	if err := VerifyChecksum(tmpPath, expectedSHA); err != nil {
		cleanup()
		return fmt.Errorf("checksum verification failed — original binary untouched: %w", err)
	}

	// 9. Replace binary (saves .bak first, then atomic rename).
	fmt.Fprintf(stdout, "Installing %s...\n", binPath)
	if err := ReplaceBinary(tmpPath, binPath); err != nil {
		cleanup()
		return fmt.Errorf("replacing binary: %w", err)
	}

	fmt.Fprintf(stdout, "\nmodel-shelf upgraded to %s\n", target)
	fmt.Fprintf(stdout, "Previous binary saved to %s.bak\n", binPath)
	return nil
}

// ExecutablePath returns the real path of the running binary,
// resolving any symlinks. Exported so the daemon upgrade endpoint can use it.
func ExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// executablePath is the internal alias kept for use within Run.
func executablePath() (string, error) { return ExecutablePath() }

// currentOrDev returns a display string for the current version.
func currentOrDev(v string) string {
	if v == "" || v == "dev" {
		return "dev (local build)"
	}
	return v
}

// readLine reads a single line from r, trimming the trailing newline.
func readLine(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}

// joinKeys returns the keys of a map[string]string joined by ", ".
func joinKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}
