//go:build linux

package daemon

import (
	"os"
)

func init() {
	daemonRestart = defaultDaemonRestart
}

// defaultDaemonRestart exits the daemon cleanly so that systemd restarts it
// via the Restart=always policy in the unit file. The unit file is rewritten
// with the new policy (and daemon-reload is run) by serviceRefreshUnit before
// this function is called, so the new policy is active before os.Exit fires.
//
// The previous approach of spawning a detached shell with Setsid=true failed
// because Setsid only creates a new session — it does not move the child to a
// different cgroup. On cgroup-v2 systemd kills every process in the service
// cgroup when the main process exits, including that shell, so the
// `systemctl start` command never ran.
func defaultDaemonRestart() {
	os.Exit(0)
}
