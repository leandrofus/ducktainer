package server

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"ducktainer/internal/agent"
	"ducktainer/internal/config"
)

// The agent binaries nodes download are built statically for every platform
// (no glibc requirement, so RHEL 8 nodes work) and named
// ducktainer-agent-<os>-<arch>[.exe]. The packages ship them in
// /usr/lib/ducktainer/agent, the MSI next to the executable.
var (
	agentOSes   = []string{"linux", "windows"}
	agentArches = []string{"amd64", "arm64"}
)

func agentBinDirs() []string {
	// DUCKTAINER_AGENT_DIR replaces the search path (custom layouts, tests)
	if d := os.Getenv("DUCKTAINER_AGENT_DIR"); d != "" {
		return []string{d}
	}
	dirs := []string{"/usr/lib/ducktainer/agent", filepath.Join(config.Dir(), "agent")}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "agent"))
	}
	return dirs
}

func agentFileName(goos, arch string) string {
	name := "ducktainer-agent-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// agentBinary returns the file to hand to a node of this platform: a static
// build when there is one, otherwise this server's own binary if it is for
// that platform.
func agentBinary(goos, arch string) (string, bool) {
	for _, dir := range agentBinDirs() {
		p := filepath.Join(dir, agentFileName(goos, arch))
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, true
		}
	}
	if goos == runtime.GOOS && arch == runtime.GOARCH && (goos == "linux" || goos == "windows") {
		if exe, err := os.Executable(); err == nil {
			return exe, true
		}
	}
	return "", false
}

// agentArchsFor lists the architectures nodes running goos can use.
func agentArchsFor(goos string) []string {
	var out []string
	for _, a := range agentArches {
		if _, ok := agentBinary(goos, a); ok {
			out = append(out, a)
		}
	}
	return out
}

// agentTargets lists every os/arch this server can provide an agent for.
func agentTargets() []string {
	out := []string{}
	for _, o := range agentOSes {
		for _, a := range agentArchsFor(o) {
			out = append(out, o+"/"+a)
		}
	}
	return out
}

// offeredArchs describes what the server can provide for goos, for the
// installers' messages.
func offeredArchs(goos string) string {
	if a := agentArchsFor(goos); len(a) > 0 {
		return strings.Join(a, ", ")
	}
	return "none (a server needs the static agent binaries in " + agentBinDirs()[0] + " for that)"
}

// serveAgentBinary hands the agent binary to an enrolled node (any agent
// token); ?os= and ?arch= pick the platform (default: linux, this server's
// architecture).
func (s *Server) serveAgentBinary(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.agentEndpoint(bearer(r))
	if !ok {
		http.Error(w, "invalid or missing token", http.StatusUnauthorized)
		return
	}
	agent.DefaultHub.SetVersion(ep.Name, r.Header.Get("X-Agent-Version"))
	goos, arch := r.URL.Query().Get("os"), r.URL.Query().Get("arch")
	if goos == "" {
		goos = "linux"
	}
	if arch == "" {
		arch = runtime.GOARCH
	}
	exe, ok := agentBinary(goos, arch)
	if !ok {
		http.Error(w, "no agent binary for "+goos+"/"+arch, http.StatusNotFound)
		return
	}
	name := "ducktainer-agent"
	if goos == "windows" {
		name += ".exe"
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, exe)
}
