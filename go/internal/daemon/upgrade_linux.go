//go:build linux

package daemon

import (
	"log"
	"os"
	"os/exec"
	"syscall"
)

func init() {
	daemonRestart = defaultDaemonRestart
}

// defaultDaemonRestart spawns a detached shell that waits for the daemon
// process to exit and then starts the service via systemctl.
//
// The detached shell runs in its own session (Setsid: true) so that it is
// placed outside the service's cgroup. Without this, systemd would send
// SIGTERM to every process in the cgroup — including the systemctl
// subprocess — when the daemon exits, preventing the start command from
// completing.
func defaultDaemonRestart() {
	cmd := exec.Command("sh", "-c", "sleep 2 && systemctl --user start model-shelf.service")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Printf("model-shelf daemon: could not spawn detached restart: %v", err)
	}
	os.Exit(0)
}
