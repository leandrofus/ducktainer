//go:build windows

package agent

import (
	"net"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

// dialEngine connects to the local container engine: a named pipe
// (\\.\pipe\docker_engine), or a unix socket path when that is what it is.
func dialEngine(path string, timeout time.Duration) (net.Conn, error) {
	if strings.HasPrefix(path, `\\.\pipe\`) || strings.HasPrefix(path, "//./pipe/") {
		return winio.DialPipe(strings.ReplaceAll(path, "/", `\`), &timeout)
	}
	return net.DialTimeout("unix", path, timeout)
}
