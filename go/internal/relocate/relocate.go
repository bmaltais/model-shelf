// Package relocate finds a shelf even if its drive was renamed or remounted.
package relocate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Shelf returns the effective shelf path, possibly relocated to a renamed drive.
//
// volumesOverride is used in tests; pass "" to use the platform default.
func Shelf(shelfRoot, volumesOverride string) string {
	if isDir(shelfRoot) {
		return shelfRoot
	}

	var volumesDir, subpath string
	if volumesOverride != "" {
		// When volumesOverride is provided (in tests), extract subpath relative to it.
		fullSubpath := extractSubpathRelativeTo(shelfRoot, volumesOverride)
		if fullSubpath == "" {
			return shelfRoot
		}
		// Remove the first component (the old volume name) to get just the inner path.
		subpath = removeFirstComponent(fullSubpath)
		volumesDir = volumesOverride
	} else {
		// In production, extract using platform-specific knowledge.
		volumesDir, subpath = extractVolumeSubpath(shelfRoot)
		if volumesDir == "" {
			return shelfRoot
		}
	}

	if subpath == "" {
		return shelfRoot
	}

	found := findSubpath(subpath, volumesDir)
	if found != "" {
		return found
	}
	return shelfRoot
}

// removeFirstComponent removes the first path component.
// For example: removeFirstComponent("OldName/shelf/models") -> "shelf/models"
func removeFirstComponent(path string) string {
	parts := strings.SplitN(path, string(filepath.Separator), 2)
	if len(parts) > 1 {
		return parts[1]
	}
	return ""
}

// extractSubpathRelativeTo extracts the subpath of shelfRoot relative to a volumes directory.
// For example: extractSubpathRelativeTo("/tmp/test/OldName/shelf/models", "/tmp/test") -> "OldName/shelf/models"
func extractSubpathRelativeTo(shelfRoot, volumesDir string) string {
	shelfRoot = filepath.Clean(shelfRoot)
	volumesDir = filepath.Clean(volumesDir)

	// Ensure volumesDir ends with a separator for proper prefix checking
	if !strings.HasSuffix(volumesDir, string(filepath.Separator)) {
		volumesDir += string(filepath.Separator)
	}

	if strings.HasPrefix(shelfRoot, volumesDir) {
		return strings.TrimPrefix(shelfRoot, volumesDir)
	}
	return ""
}

// extractVolumeSubpath returns the volumes root dir and the subpath under the
// volume name, or ("", "") if shelfRoot is not under a recognized volume root.
func extractVolumeSubpath(shelfRoot string) (volumesDir, subpath string) {
	// macOS: /Volumes/<name>/<sub>
	if strings.HasPrefix(shelfRoot, "/Volumes/") {
		rest := strings.TrimPrefix(shelfRoot, "/Volumes/")
		parts := strings.SplitN(rest, string(filepath.Separator), 2)
		if len(parts) == 2 {
			return "/Volumes", parts[1]
		}
		return "", ""
	}
	// Linux: /media/<user>/<name>/<sub> or /mnt/<name>/<sub>
	if runtime.GOOS == "linux" {
		for _, prefix := range linuxVolumePrefixes() {
			if strings.HasPrefix(shelfRoot, prefix+"/") {
				rest := strings.TrimPrefix(shelfRoot, prefix+"/")
				parts := strings.SplitN(rest, string(filepath.Separator), 2)
				if len(parts) == 2 {
					return prefix, parts[1]
				}
			}
		}
	}
	return "", ""
}

func linuxVolumePrefixes() []string {
	var prefixes []string
	if user := os.Getenv("USER"); user != "" {
		prefixes = append(prefixes, "/media/"+user)
	}
	prefixes = append(prefixes, "/mnt")
	return prefixes
}

func findSubpath(subpath, volumesDir string) string {
	entries, err := os.ReadDir(volumesDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		candidate := filepath.Join(volumesDir, e.Name(), subpath)
		if isDir(candidate) {
			return candidate
		}
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
