// Package relocate handles shelf discovery when drives are renamed or remounted.
package relocate

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// RelocateShelf returns the effective shelf_root, possibly relocated to a renamed drive.
func RelocateShelf(shelfRoot string) string {
	if info, err := os.Stat(shelfRoot); err == nil && info.IsDir() {
		return shelfRoot
	}

	volumesDir, subpath := extractVolumeSubpath(shelfRoot)
	if volumesDir == "" {
		return shelfRoot
	}

	found := findShelfAtSubpath(subpath, volumesDir)
	if found != "" {
		return found
	}
	return shelfRoot
}

// extractVolumeSubpath checks if the path is under /Volumes/<name>/... and returns
// the volumes dir and the subpath under the volume.
func extractVolumeSubpath(shelfRoot string) (string, string) {
	if runtime.GOOS != "darwin" {
		return "", ""
	}
	clean := filepath.Clean(shelfRoot)
	parts := strings.Split(clean, string(filepath.Separator))
	// Expected: ["", "Volumes", "<name>", "sub", "path", ...]
	if len(parts) < 4 || parts[0] != "" || parts[1] != "Volumes" {
		return "", ""
	}
	subpath := strings.Join(parts[3:], string(filepath.Separator))
	return "/Volumes", subpath
}

func findShelfAtSubpath(subpath, volumesDir string) string {
	if volumesDir == "" {
		return ""
	}
	entries, err := os.ReadDir(volumesDir)
	if err != nil {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		volPath := filepath.Join(volumesDir, e.Name())
		info, err := os.Stat(volPath)
		if err != nil || !info.IsDir() {
			continue
		}
		candidate := filepath.Join(volPath, subpath)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}
