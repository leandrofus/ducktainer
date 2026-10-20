// Package dockersock finds the local container engine's socket without
// asking the user: Docker, rootless Docker, Podman (system or rootless), or
// whatever DOCKER_HOST / CONTAINER_HOST points at.
package dockersock

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Default is where Docker (and podman-docker) listen.
const Default = "/var/run/docker.sock"

type Candidate struct {
	Path   string
	Source string // how it was found, for messages
}

// Candidates lists the places a Docker-compatible daemon may listen, most
// specific first. The paths may not exist.
func Candidates() []Candidate {
	var out []Candidate
	add := func(p, src string) {
		if p != "" {
			out = append(out, Candidate{p, src})
		}
	}
	for _, v := range []string{"DOCKER_HOST", "CONTAINER_HOST"} {
		if p, ok := strings.CutPrefix(os.Getenv(v), "unix://"); ok {
			add(p, v)
		}
	}
	add(Default, "standard path")
	add("/run/docker.sock", "standard path")
	add("/run/podman/podman.sock", "Podman")
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		add(filepath.Join(dir, "docker.sock"), "rootless Docker")
		add(filepath.Join(dir, "podman", "podman.sock"), "rootless Podman")
	}
	uid := strconv.Itoa(os.Getuid())
	add("/run/user/"+uid+"/docker.sock", "rootless Docker")
	add("/run/user/"+uid+"/podman/podman.sock", "rootless Podman")
	// running as root (the service): a single user's rootless daemon
	if m, _ := filepath.Glob("/run/user/*/docker.sock"); len(m) == 1 {
		add(m[0], "rootless Docker")
	}
	if m, _ := filepath.Glob("/run/user/*/podman/podman.sock"); len(m) == 1 {
		add(m[0], "rootless Podman")
	}
	return out
}

// IsSocket reports whether path is a unix socket (symlinks followed, so
// podman-docker's /var/run/docker.sock counts).
func IsSocket(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode()&fs.ModeSocket != 0
}

// Find returns the first candidate that exists, or "".
func Find() string {
	for _, c := range Candidates() {
		if IsSocket(c.Path) {
			return c.Path
		}
	}
	return ""
}

// Path is Find, falling back to Default when nothing exists yet (the daemon
// may still be starting).
func Path() string {
	if p := Find(); p != "" {
		return p
	}
	return Default
}
