// Package meshconfig handles the mesh-aware configuration at ~/.model-shelf/.
package meshconfig

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	DefaultPort = 8844
	DirName     = ".model-shelf"
)

// GPUConfig holds manual GPU override settings from [gpu] in config.toml.
type GPUConfig struct {
	Name        string  `toml:"name"`
	VRAMTotalGB float64 `toml:"vram_total_gb"`
}

// Config holds the mesh node configuration.
type Config struct {
	Name         string     `toml:"name"`
	Port         int        `toml:"port"`
	Roles        []string   `toml:"roles"`
	ShelfRoot    string     `toml:"shelf_root"`
	MeshKey      string     `toml:"-"` // loaded separately from mesh.key file
	Seeds        []string   `toml:"seeds,omitempty"`
	GPU          *GPUConfig `toml:"gpu,omitempty"`
	PreferSource string     `toml:"prefer_source,omitempty"` // "auto" (default), "hf", "peer"
	Version      string     `toml:"-"` // set at runtime from build-time version variable; never persisted
}

// ConfigDir returns the path to ~/.model-shelf/.
func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, DirName)
}

// ConfigPath returns the path to ~/.model-shelf/config.toml.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.toml")
}

// MeshKeyPath returns the path to ~/.model-shelf/mesh.key.
func MeshKeyPath() string {
	return filepath.Join(ConfigDir(), "mesh.key")
}

// StateDir returns ~/.model-shelf/state/.
func StateDir() string {
	return filepath.Join(ConfigDir(), "state")
}

// MeshStatePath returns the path to ~/.model-shelf/state/mesh.json.
func MeshStatePath() string {
	return filepath.Join(StateDir(), "mesh.json")
}

// Exists returns true if ~/.model-shelf/config.toml exists.
func Exists() bool {
	_, err := os.Stat(ConfigPath())
	return err == nil
}

// Load reads the config from ~/.model-shelf/config.toml and mesh.key.
func Load() (*Config, error) {
	return LoadFrom(ConfigPath())
}

// LoadFrom reads a config from a specific path.
func LoadFrom(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	// Expand ~ in shelf_root.
	if strings.HasPrefix(cfg.ShelfRoot, "~/") {
		home, _ := os.UserHomeDir()
		cfg.ShelfRoot = filepath.Join(home, cfg.ShelfRoot[2:])
	}
	// Load mesh key if present.
	keyPath := filepath.Join(filepath.Dir(path), "mesh.key")
	if data, err := os.ReadFile(keyPath); err == nil {
		cfg.MeshKey = strings.TrimSpace(string(data))
	}
	return &cfg, nil
}

// Write writes the config to ~/.model-shelf/config.toml.
func Write(cfg *Config) error {
	return WriteTo(ConfigPath(), cfg)
}

// WriteTo writes the config to a specific path.
func WriteTo(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	return enc.Encode(cfg)
}

// GenerateMeshKey creates a random 32-byte hex mesh key and writes it to the default path.
func GenerateMeshKey() (string, error) {
	return GenerateMeshKeyAt(MeshKeyPath())
}

// GenerateMeshKeyAt creates a random 32-byte hex mesh key and writes it to path.
func GenerateMeshKeyAt(path string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	key := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return "", err
	}
	return key, nil
}

// WriteMeshKey writes a known mesh key to the default path (used when joining a mesh).
func WriteMeshKey(key string) error {
	return WriteMeshKeyAt(MeshKeyPath(), key)
}

// WriteMeshKeyAt writes a known mesh key to path.
func WriteMeshKeyAt(path string, key string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(key+"\n"), 0o600)
}

// GetHostname returns the system hostname (used as default node name).
func GetHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return name
}
