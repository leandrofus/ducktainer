//go:build !windows

package agent

import (
	"os"
	"syscall"
)

// execSelf replaces this process with the (new) binary, keeping the pid, so
// the service manager sees no restart.
var execSelf = func(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
