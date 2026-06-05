//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"strings"
)

const serviceName = "ModelShelfDaemon"
const serviceDisplayName = "Model Shelf Daemon"

func install(exePath string) error {
	// Create the Windows service using sc.exe.
	binPath := fmt.Sprintf(`"%s" daemon`, exePath)
	cmd := exec.Command("sc", "create", serviceName,
		"binPath=", binPath,
		"DisplayName=", serviceDisplayName,
		"start=", "auto",
		"obj=", "NT AUTHORITY\\NetworkService",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sc create: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	// Set description.
	descCmd := exec.Command("sc", "description", serviceName, "Model Shelf mesh daemon for model resolution and coordination")
	_ = descCmd.Run()

	return nil
}

func uninstall() error {
	// Stop first (ignore error if not running).
	stopCmd := exec.Command("sc", "stop", serviceName)
	_ = stopCmd.Run()

	// Delete the service.
	cmd := exec.Command("sc", "delete", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sc delete: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	return nil
}

func start() error {
	cmd := exec.Command("sc", "start", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sc start: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func stop() error {
	cmd := exec.Command("sc", "stop", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sc stop: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func getStatus() (*Status, error) {
	s := &Status{}

	cmd := exec.Command("sc", "query", serviceName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Service not found — not installed.
		return s, nil
	}

	output := string(out)
	s.Installed = true
	s.Detail = strings.TrimSpace(output)

	if strings.Contains(output, "RUNNING") {
		s.Running = true
	}

	return s, nil
}
