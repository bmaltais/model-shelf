//go:build !linux

package daemon

import (
	"log"
	"os"

	"github.com/alexziskind1/model-shelf/internal/service"
)

func init() {
	daemonRestart = defaultDaemonRestart
}

// defaultDaemonRestart restarts the service via the platform service manager.
// On non-Linux platforms the cgroup-kill problem described in #223 does not
// apply, so a direct service.Restart() followed by os.Exit is safe.
func defaultDaemonRestart() {
	if err := service.Restart(); err != nil {
		log.Printf("model-shelf daemon: could not restart service after upgrade: %v", err)
	}
	os.Exit(0)
}
