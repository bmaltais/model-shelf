//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const serviceName = "com.model-shelf.daemon"

// plistDir returns the user LaunchAgents directory.
func plistDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

// plistPath returns the full path to the plist file.
func plistPath() string {
	return filepath.Join(plistDir(), serviceName+".plist")
}

// plistContent generates the launchd plist XML.
func plistContent(exePath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>daemon</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>/tmp/model-shelf.stdout.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/model-shelf.stderr.log</string>
</dict>
</plist>
`, serviceName, exePath)
}

func install(exePath string) error {
	dir := plistDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents dir: %w", err)
	}

	content := plistContent(exePath)
	if err := os.WriteFile(plistPath(), []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing plist file: %w", err)
	}

	// Load the service (enables and starts on user login).
	if err := launchctl("load", "-w", plistPath()); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	return nil
}

func uninstall() error {
	path := plistPath()

	// Unload (stops and disables).
	_ = launchctl("unload", "-w", path)

	// Remove plist file.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing plist file: %w", err)
	}

	return nil
}

// refreshUnit is a no-op on macOS: launchd reads the plist directly on
// each load and there is no separate daemon-reload step.
func refreshUnit(_ string) error { return nil }

func start() error {
	return launchctl("start", serviceName)
}

func stop() error {
	return launchctl("stop", serviceName)
}

func getStatus() (*Status, error) {
	s := &Status{}

	// Check if plist file exists.
	if _, err := os.Stat(plistPath()); os.IsNotExist(err) {
		return s, nil
	}
	s.Installed = true

	// Check if running via launchctl list.
	out, err := launchctlOutput("list")
	if err == nil && strings.Contains(out, serviceName) {
		// Parse the line to see if PID is present (running).
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, serviceName) {
				fields := strings.Fields(line)
				if len(fields) >= 1 && fields[0] != "-" {
					s.Running = true
				}
				s.Detail = line
				break
			}
		}
	}

	return s, nil
}

// launchctl runs a launchctl command.
func launchctl(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// launchctlOutput runs a launchctl command and returns stdout.
func launchctlOutput(args ...string) (string, error) {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.Output()
	return string(out), err
}
