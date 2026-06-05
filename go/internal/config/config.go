// Package config handles loading and writing Model Shelf configuration.
//
// Lookup order:
//  1. Explicit path passed to LoadConfig().
//  2. $MODEL_SHELF_CONFIG.
//  3. ~/.config/model-shelf/config.toml (user-level default).
//  4. None of the above -> bootstrap a default at (3) and load it.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/alexziskind1/model-shelf/internal/meshconfig"
	"github.com/alexziskind1/model-shelf/internal/relocate"
	"github.com/alexziskind1/model-shelf/internal/resolver"
)

// UserConfigPath returns the default user-level config path.
func UserConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "model-shelf", "config.toml")
}

type tomlConfig struct {
	ShelfRoot      *string `toml:"shelf_root"`
	AllowDownloads *bool   `toml:"allow_downloads"`
}

func readConfig(path string) (*resolver.Config, error) {
	var tc tomlConfig
	if _, err := toml.DecodeFile(path, &tc); err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	cfg := &resolver.Config{
		AllowDownloads: true,
	}
	if tc.AllowDownloads != nil {
		cfg.AllowDownloads = *tc.AllowDownloads
	}
	if tc.ShelfRoot != nil {
		root := *tc.ShelfRoot
		if strings.HasPrefix(root, "~/") {
			home, _ := os.UserHomeDir()
			root = filepath.Join(home, root[2:])
		}
		cfg.ShelfRoot = root
	}
	return cfg, nil
}

// WriteConfig writes a config file at path.
func WriteConfig(path string, shelfRoot string, allowDownloads bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var lines []string
	lines = append(lines, "# Model Shelf config.")
	lines = append(lines, "# shelf_root is optional. If omitted, the primary is auto-discovered")
	lines = append(lines, "# from any mounted /Volumes/*/ModelShelf/models, falling back to")
	lines = append(lines, "# ~/.cache/model-shelf/models. Set it explicitly to pin a location.")
	if shelfRoot != "" {
		lines = append(lines, fmt.Sprintf("shelf_root      = %q", shelfRoot))
	}
	if allowDownloads {
		lines = append(lines, "allow_downloads = true")
	} else {
		lines = append(lines, "allow_downloads = false")
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// WritableConfigPath returns where the config should be written.
func WritableConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("MODEL_SHELF_CONFIG"); env != "" {
		return env
	}
	return UserConfigPath()
}

// BootstrapDefaultConfig creates a default config at path if missing.
func BootstrapDefaultConfig(path string) (string, error) {
	if path == "" {
		path = UserConfigPath()
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := WriteConfig(path, "", true); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr,
		"model-shelf: created default config at %s\n"+
			"model-shelf: run `model-shelf init` to create your shelf.\n", path)
	return path, nil
}

// LoadConfig loads configuration with full discovery logic.
// It checks for a mesh config (~/.model-shelf/config.toml) first —
// if it exists, shelf_root is read from there, avoiding legacy config creation.
func LoadConfig(path string) (*resolver.Config, error) {
	cfg, err := loadRaw(path)
	if err != nil {
		return nil, err
	}
	if cfg.ShelfRoot == "" {
		cfg.ShelfRoot = resolver.DiscoverPrimaryShelf()
	} else {
		cfg.ShelfRoot = relocate.RelocateShelf(cfg.ShelfRoot)
	}
	return cfg, nil
}

func loadRaw(path string) (*resolver.Config, error) {
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return readConfig(path)
		}
		bootstrapped, err := BootstrapDefaultConfig(path)
		if err != nil {
			return nil, err
		}
		return readConfig(bootstrapped)
	}
	if env := os.Getenv("MODEL_SHELF_CONFIG"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return readConfig(env)
		}
		bootstrapped, err := BootstrapDefaultConfig(env)
		if err != nil {
			return nil, err
		}
		return readConfig(bootstrapped)
	}
	// Check mesh config first — if it exists, use its shelf_root
	// to avoid creating a legacy config at ~/.config/model-shelf/.
	if meshconfig.Exists() {
		meshCfg, err := loadFromMeshConfig()
		if err != nil {
			return nil, fmt.Errorf("mesh config exists but cannot be loaded: %w", err)
		}
		return meshCfg, nil
	}
	userCfg := UserConfigPath()
	if _, err := os.Stat(userCfg); err == nil {
		return readConfig(userCfg)
	}
	bootstrapped, err := BootstrapDefaultConfig("")
	if err != nil {
		return nil, err
	}
	return readConfig(bootstrapped)
}

// loadFromMeshConfig loads resolver config from the mesh config
// (~/.model-shelf/config.toml). Caller must verify meshconfig.Exists() first.
// Returns an error if the config cannot be parsed or has no shelf_root.
func loadFromMeshConfig() (*resolver.Config, error) {
	mc, err := meshconfig.Load()
	if err != nil {
		return nil, err
	}
	if mc.ShelfRoot == "" {
		return nil, fmt.Errorf("mesh config has no shelf_root")
	}
	return &resolver.Config{
		ShelfRoot:      mc.ShelfRoot,
		AllowDownloads: true,
	}, nil
}
