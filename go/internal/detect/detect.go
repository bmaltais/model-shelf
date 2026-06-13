// Package detect finds plausible Model Shelf locations on the current machine.
package detect

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Candidate describes a potential shelf location.
type Candidate struct {
	Path       string // full shelf_root path
	Label      string // short display name
	Existing   bool   // does ModelShelf/models already exist here?
	IsExternal bool   // external drive vs internal
}

// Candidates returns ranked shelf candidates for the current machine.
// Order: existing external first, then new external, then internal fallback.
func Candidates() []Candidate {
	home, _ := os.UserHomeDir()
	return CandidatesFrom(externalVolumesDir(), home)
}

// CandidatesFrom is the testable core of Candidates.
// volumesDir is the directory containing external volumes (empty = skip external scan).
func CandidatesFrom(volumesDir, home string) []Candidate {
	var candidates []Candidate

	if volumesDir != "" {
		candidates = append(candidates, scanExternal(volumesDir)...)
	}

	internal := Candidate{
		Path:       filepath.Join(home, ".cache", "model-shelf", "models"),
		Label:      "internal",
		Existing:   isDir(filepath.Join(home, ".cache", "model-shelf", "models")),
		IsExternal: false,
	}
	candidates = append(candidates, internal)

	sort.SliceStable(candidates, func(i, j int) bool {
		// Existing before new; external before internal within same group.
		ei, ej := candidates[i].Existing, candidates[j].Existing
		if ei != ej {
			return ei
		}
		return candidates[i].IsExternal && !candidates[j].IsExternal
	})
	return candidates
}

func scanExternal(volumesDir string) []Candidate {
	entries, err := os.ReadDir(volumesDir)
	if err != nil {
		return nil
	}
	var out []Candidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip macOS Macintosh HD symlink and hidden entries.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		shelfPath := filepath.Join(volumesDir, e.Name(), "ModelShelf", "models")
		out = append(out, Candidate{
			Path:       shelfPath,
			Label:      e.Name(),
			Existing:   isDir(shelfPath),
			IsExternal: true,
		})
	}
	return out
}

func externalVolumesDir() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Volumes"
	case "linux":
		if user := os.Getenv("USER"); user != "" {
			if isDir("/media/" + user) {
				return "/media/" + user
			}
		}
		if isDir("/mnt") {
			return "/mnt"
		}
		return ""
	default:
		return ""
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
