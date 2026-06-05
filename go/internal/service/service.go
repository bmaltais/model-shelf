// Package service manages the model-shelf daemon as a system service.
// On Linux it uses systemd user units, on macOS launchd plists,
// and on Windows the Windows Service Control Manager.
package service

import "fmt"

// Status represents the current state of the service.
type Status struct {
	Installed bool
	Running   bool
	Detail    string // platform-specific status detail
}

// String returns a human-readable status.
func (s Status) String() string {
	if !s.Installed {
		return "not installed"
	}
	if s.Running {
		return "running"
	}
	return "stopped"
}

// Install creates and enables the service to auto-start on boot.
// The service runs `model-shelf daemon` as the current user.
func Install() error {
	exe, err := executablePath()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	return install(exe)
}

// Uninstall stops and removes the service.
func Uninstall() error {
	return uninstall()
}

// Start starts the service.
func Start() error {
	return start()
}

// Stop stops the service.
func Stop() error {
	return stop()
}

// GetStatus returns the current service status.
func GetStatus() (*Status, error) {
	return getStatus()
}
