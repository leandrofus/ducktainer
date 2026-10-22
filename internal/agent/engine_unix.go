//go:build !windows

package agent

import (
	"net"
	"time"
)

// dialEngine connects to the local container engine: a unix socket.
func dialEngine(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", path, timeout)
}
