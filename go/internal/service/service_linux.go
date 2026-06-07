//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const serviceName = "model-shelf"

// unitDir returns the user systemd unit directory.
func unitDir() string {
	// XDG_CONFIG_HOME or ~/.config
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "systemd", "user")
}

// unitPath returns the full path to the unit file.
func unitPath() string {
	return filepath.Join(unitDir(), serviceName+".service")
}

// unitContent generates the systemd unit file content.
func unitContent(exePath string) string {
	return fmt.Sprintf(`[Unit]
Description=Model Shelf Daemon
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart="%s" daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, exePath)
}

func install(exePath string) error {
	dir := unitDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating systemd user dir: %w", err)
	}

	content := unitContent(exePath)
	if err := os.WriteFile(unitPath(), []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing unit file: %w", err)
	}

	// Reload systemd to pick up the new unit.
	if err := systemctl("daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}

	// Enable for auto-start on boot.
	if err := systemctl("enable", serviceName+".service"); err != nil {
		return fmt.Errorf("enable: %w", err)
	}

	// Start the service immediately.
	if err := systemctl("start", serviceName+".service"); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	return nil
}

func uninstall() error {
	// Stop first (ignore error if not running).
	_ = systemctl("stop", serviceName+".service")

	// Disable.
	_ = systemctl("disable", serviceName+".service")

	// Remove unit file.
	path := unitPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing unit file: %w", err)
	}

	// Reload.
	_ = systemctl("daemon-reload")

	return nil
}

func start() error {
	return systemctl("start", serviceName+".service")
}

func stop() error {
	return systemctl("stop", serviceName+".service")
}

func getStatus() (*Status, error) {
	s := &Status{}

	// Check if unit file exists.
	if _, err := os.Stat(unitPath()); os.IsNotExist(err) {
		return s, nil
	}
	s.Installed = true

	// Check if active.
	out, err := systemctlOutput("is-active", serviceName+".service")
	if err == nil && strings.TrimSpace(out) == "active" {
		s.Running = true
	}

	// Get detailed status.
	detail, _ := systemctlOutput("status", serviceName+".service")
	s.Detail = detail

	return s, nil
}

// systemctl runs a systemctl --user command.
func systemctl(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// systemctlOutput runs a systemctl --user command and returns stdout.
func systemctlOutput(args ...string) (string, error) {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}
