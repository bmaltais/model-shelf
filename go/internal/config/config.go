// Package config handles loading and writing Model Shelf configuration.
//
// Lookup order:
//  1. Explicit path passed to LoadRaw.
//  2. $MODEL_SHELF_CONFIG.
//  3. ~/.config/model-shelf/config.toml (user-level default).
//  4. None of the above — bootstrap a default at (3).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds the runtime configuration.
// ShelfRoot is empty string when not pinned (auto-discovery will fill it in).
type Config struct {
	ShelfRoot      string
	AllowDownloads bool
}

type tomlConfig struct {
	ShelfRoot      *string `toml:"shelf_root"`
	AllowDownloads *bool   `toml:"allow_downloads"`
}

// UserConfigPath returns the default user-level config path.
func UserConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "model-shelf", "config.toml")
}

// WritableConfigPath returns where `model-shelf init` should write the config.
func WritableConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("MODEL_SHELF_CONFIG"); env != "" {
		return env
	}
	return UserConfigPath()
}

// LoadRaw reads a config file and returns a Config.
// If path is empty, the standard lookup order is used.
// ShelfRoot may be empty if not set in the file.
func LoadRaw(path string) (*Config, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return nil, err
	}
	return readFile(resolved)
}

func resolvePath(path string) (string, error) {
	if path != "" {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return BootstrapDefault(path)
		}
		return path, nil
	}
	if env := os.Getenv("MODEL_SHELF_CONFIG"); env != "" {
		if _, err := os.Stat(env); os.IsNotExist(err) {
			return BootstrapDefault(env)
		}
		return env, nil
	}
	ucp := UserConfigPath()
	if _, err := os.Stat(ucp); os.IsNotExist(err) {
		return BootstrapDefault(ucp)
	}
	return ucp, nil
}

func readFile(path string) (*Config, error) {
	var tc tomlConfig
	if _, err := toml.DecodeFile(path, &tc); err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	cfg := &Config{AllowDownloads: true}
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

// WriteConfig writes a config file at path. Creates parent directories as needed.
// Pass empty string for shelfRoot to omit the field (auto-discovery at runtime).
func WriteConfig(path, shelfRoot string, allowDownloads bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lines := []string{
		"# Model Shelf config.",
		"# shelf_root is optional. If omitted, the primary is auto-discovered",
		"# from any mounted external drive with ModelShelf/models, falling back to",
		"# ~/.cache/model-shelf/models. Set it explicitly to pin a location.",
	}
	if shelfRoot != "" {
		lines = append(lines, fmt.Sprintf(`shelf_root      = %q`, shelfRoot))
	}
	al := "true"
	if !allowDownloads {
		al = "false"
	}
	lines = append(lines, fmt.Sprintf("allow_downloads = %s", al))
	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

// BootstrapDefault creates a default config at path if it doesn't exist.
// Returns path unchanged.
func BootstrapDefault(path string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := WriteConfig(path, "", true); err != nil {
		return "", fmt.Errorf("bootstrapping config at %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr,
		"model-shelf: created default config at %s\n"+
			"model-shelf: run `model-shelf init` to create your shelf.\n", path)
	return path, nil
}
