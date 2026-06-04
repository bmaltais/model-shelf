// Package detect scans for plausible Model Shelf locations.
package detect

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// StorageCandidate represents a potential shelf location.
type StorageCandidate struct {
	Path       string
	Label      string
	Existing   bool
	IsExternal bool
}

// DetectStorageCandidates returns ranked candidates for shelf placement.
func DetectStorageCandidates() []StorageCandidate {
	return DetectStorageCandidatesAt("", "")
}

// DetectStorageCandidatesAt allows overriding volumes and home dirs (for testing).
func DetectStorageCandidatesAt(volumesDir, homeDir string) []StorageCandidate {
	if volumesDir == "" {
		if runtime.GOOS == "darwin" {
			volumesDir = "/Volumes"
		}
	}
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}

	var candidates []StorageCandidate

	if volumesDir != "" {
		if entries, err := os.ReadDir(volumesDir); err == nil {
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
				shelfPath := filepath.Join(volPath, "ModelShelf", "models")
				_, existErr := os.Stat(shelfPath)
				candidates = append(candidates, StorageCandidate{
					Path:       shelfPath,
					Label:      e.Name(),
					Existing:   existErr == nil,
					IsExternal: true,
				})
			}
		}
	}

	internal := filepath.Join(homeDir, ".cache", "model-shelf", "models")
	_, existErr := os.Stat(internal)
	candidates = append(candidates, StorageCandidate{
		Path:       internal,
		Label:      "internal",
		Existing:   existErr == nil,
		IsExternal: false,
	})

	// Sort: existing first, external before internal within each group.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Existing != candidates[j].Existing {
			return candidates[i].Existing
		}
		return candidates[i].IsExternal && !candidates[j].IsExternal
	})

	return candidates
}
