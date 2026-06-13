package relocate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bmaltais/model-shelf/internal/relocate"
)

func TestRelocate_PathExists(t *testing.T) {
	dir := t.TempDir()
	result := relocate.Shelf(dir, "")
	if result != dir {
		t.Errorf("existing path should be returned unchanged: got %q", result)
	}
}

func TestRelocate_NotUnderVolume(t *testing.T) {
	// Path doesn't exist but isn't under a volume root — return unchanged.
	result := relocate.Shelf("/nonexistent/path/to/shelf", "")
	if result != "/nonexistent/path/to/shelf" {
		t.Errorf("non-volume path should be returned unchanged: got %q", result)
	}
}

func TestRelocate_FindsOnRenamedDrive(t *testing.T) {
	volumes := t.TempDir()
	// Simulate: shelf was at volumes/OldName/ModelShelf/models
	// Drive was renamed to NewName.
	newVol := filepath.Join(volumes, "NewName")
	newShelf := filepath.Join(newVol, "ModelShelf", "models")
	os.MkdirAll(newShelf, 0o755)

	// The stored config path points to OldName which doesn't exist.
	oldShelf := filepath.Join(volumes, "OldName", "ModelShelf", "models")

	result := relocate.Shelf(oldShelf, volumes)
	if result != newShelf {
		t.Errorf("expected relocated path %q, got %q", newShelf, result)
	}
}

func TestRelocate_NotFoundReturnsOriginal(t *testing.T) {
	volumes := t.TempDir()
	// No volumes have ModelShelf/models.
	os.MkdirAll(filepath.Join(volumes, "EmptyDrive"), 0o755)

	oldShelf := filepath.Join(volumes, "OldName", "ModelShelf", "models")
	result := relocate.Shelf(oldShelf, volumes)
	if result != oldShelf {
		t.Errorf("expected original path returned, got %q", result)
	}
}
