package main

import (
	"fmt"
	"os"

	"github.com/alexziskind1/model-shelf/internal/selfupgrade"
	"github.com/alexziskind1/model-shelf/internal/service"
)

func cmdUpgrade(args []string) int {
	_, flags := parseFlags(args)

	if flags["help"] == "true" {
		fmt.Println("Usage: model-shelf upgrade [--version x.y.z] [--yes] [--force]")
		fmt.Println()
		fmt.Println("Fetch the latest release from GitHub, verify its SHA256 checksum,")
		fmt.Println("and atomically replace the running binary.")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --version <x.y.z>  Pin upgrade to a specific release (default: latest)")
		fmt.Println("  --yes              Skip confirmation prompt")
		fmt.Println("  --force            Proceed even if already running the target version")
		return 0
	}

	targetVersion := flags["version"]
	yes := flags["yes"] == "true"
	force := flags["force"] == "true"

	err := selfupgrade.Run(targetVersion, version, yes, force, os.Stdout, os.Stderr, os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// After a successful upgrade, attempt to restart the service.
	// If the service isn't installed, print a reminder instead.
	status, statusErr := service.GetStatus()
	if statusErr == nil && status.Installed {
		if restartErr := service.Restart(); restartErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not restart service: %v\n", restartErr)
			fmt.Fprintf(os.Stderr, "  Run 'model-shelf service restart' to apply the upgrade.\n")
		} else {
			fmt.Println("Service restarted successfully.")
		}
	} else {
		fmt.Println()
		fmt.Println("Reminder: restart the daemon to apply the upgrade:")
		fmt.Println("  model-shelf service restart")
		fmt.Println("  — or —")
		fmt.Println("  model-shelf daemon")
	}

	return 0
}
