//go:build windows

package agent

import (
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
)

// execSelf restarts the agent into the new binary. Windows cannot replace a
// process image, so under the Service Control Manager the process exits with
// an error and the service's recovery options (installed with the service)
// start it again; started by hand it only asks for a restart.
var execSelf = func(string) error {
	if ok, _ := svc.IsWindowsService(); !ok {
		return errUpdatedRestartManually
	}
	time.Sleep(time.Second) // let the log line out
	os.Exit(1)
	return nil
}
