package detect_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bmaltais/model-shelf/internal/detect"
)

func TestCandidates_InternalFallback(t *testing.T) {
	home := t.TempDir()
	// No external volumes dir — should return internal fallback only.
	candidates := detect.CandidatesFrom("", home)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]
	if c.IsExternal {
		t.Error("fallback candidate should not be external")
	}
	want := filepath.Join(home, ".cache", "model-shelf", "models")
	if c.Path != want {
		t.Errorf("Path = %q, want %q", c.Path, want)
	}
}

func TestCandidates_ExistingExternalFirst(t *testing.T) {
	volumes := t.TempDir()
	home := t.TempDir()

	// Create two external volumes, one with an existing shelf.
	vol1 := filepath.Join(volumes, "Aardvark")
	vol2 := filepath.Join(volumes, "Zebra")
	shelf2 := filepath.Join(vol2, "ModelShelf", "models")
	os.MkdirAll(vol1, 0o755)
	os.MkdirAll(shelf2, 0o755)

	candidates := detect.CandidatesFrom(volumes, home)

	// Should be: existing external (Zebra) first, then new external (Aardvark), then internal.
	if len(candidates) < 2 {
		t.Fatalf("expected at least 2 candidates, got %d", len(candidates))
	}
	if !candidates[0].Existing || !candidates[0].IsExternal {
		t.Errorf("first candidate should be existing external, got %+v", candidates[0])
	}
	if candidates[0].Label != "Zebra" {
		t.Errorf("first candidate label = %q, want Zebra", candidates[0].Label)
	}
}

func TestCandidates_ExistingFlagSet(t *testing.T) {
	volumes := t.TempDir()
	home := t.TempDir()

	vol := filepath.Join(volumes, "MyDrive")
	shelf := filepath.Join(vol, "ModelShelf", "models")
	os.MkdirAll(shelf, 0o755)

	candidates := detect.CandidatesFrom(volumes, home)
	var found *detect.Candidate
	for i := range candidates {
		if candidates[i].Label == "MyDrive" {
			found = &candidates[i]
			break
		}
	}
	if found == nil {
		t.Fatal("candidate for MyDrive not found")
	}
	if !found.Existing {
		t.Error("Existing should be true for a volume that has a shelf")
	}
	if found.Path != shelf {
		t.Errorf("Path = %q, want %q", found.Path, shelf)
	}
}
