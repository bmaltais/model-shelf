package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

// meshStatePath returns the path to ~/.model-shelf/state/mesh.json.
func meshStatePath() string {
	return filepath.Join(meshconfig.StateDir(), "mesh.json")
}

// saveMeshState persists the node list to disk.
func saveMeshState(nodes []MeshNode) error {
	path := meshStatePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// loadMeshState loads the node list from disk.
// Returns nil (no error) if the file doesn't exist.
func loadMeshState() ([]MeshNode, error) {
	data, err := os.ReadFile(meshStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var nodes []MeshNode
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}
