package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"ducktainer/internal/access"
	"ducktainer/internal/agent"
	"ducktainer/internal/config"
)

var agentUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 << 10,
	WriteBufferSize: 32 << 10,
	CheckOrigin: func(r *http.Request) bool {
		return true // agents connect from any origin / IP
	},
}

// bearer returns the token sent in the Authorization header.
func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// agentEndpoint returns the agent endpoint whose own token is token. The
// admin token is not accepted: a node keeps its token on disk, so it must
// only ever unlock that node.
func (s *Server) agentEndpoint(token string) (config.Endpoint, bool) {
	if token == "" {
		return config.Endpoint{}, false
	}
	for _, ep := range s.Cfg.Snapshot().Endpoints {
		if ep.Agent() && ep.Token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(ep.Token)) == 1 {
			if s.Cfg.CheckToken(token) {
				// registered by an earlier version with the admin token
				log.Printf("[agent] %q uses the admin token: give the host its own token in Settings → Hosts and reinstall the agent", ep.Name)
				return config.Endpoint{}, false
			}
			return ep, true
		}
	}
	return config.Endpoint{}, false
}

// agentPins returns the server certificate's sha256 fingerprint (hex, for
// `ducktainer agent --fingerprint`) and its public key pin (for curl
// --pinnedpubkey); both are empty without TLS.
func (s *Server) agentPins() (fp, pubkey string) {
	if len(s.TLSCert) == 0 {
		return "", ""
	}
	sum := sha256.Sum256(s.TLSCert)
	fp = hex.EncodeToString(sum[:])
	if cert, err := x509.ParseCertificate(s.TLSCert); err == nil {
		k := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
		pubkey = "sha256//" + base64.StdEncoding.EncodeToString(k[:])
	}
	return fp, pubkey
}

// shellQuote quotes v for a POSIX shell.
func shellQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }

// validServerURL accepts an absolute http(s) URL with a host.
func validServerURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.RawQuery == "" && u.Fragment == ""
}

// agentBinDirs hold the agent binaries built for every architecture and
// statically (no glibc), named ducktainer-agent-linux-<arch>: the packages
// ship them in the first one. A node of any architecture, and one with an old
// glibc (RHEL 8), gets one of these instead of this server's own binary.
func agentBinDirs() []string {
	return []string{"/usr/lib/ducktainer/agent", filepath.Join(config.Dir(), "agent")}
}

// agentBinary returns the file to hand to a node of this architecture: a
// static build when there is one, otherwise this server's own binary if it is
// for that architecture.
func agentBinary(arch string) (string, bool) {
	for _, dir := range agentBinDirs() {
		p := filepath.Join(dir, "ducktainer-agent-linux-"+arch)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, true
		}
	}
	if arch == runtime.GOARCH && runtime.GOOS == "linux" {
		if exe, err := os.Executable(); err == nil {
			return exe, true
		}
	}
	return "", false
}

// agentArchs lists the architectures nodes can run.
func agentArchs() []string {
	var out []string
	for _, a := range []string{"amd64", "arm64"} {
		if _, ok := agentBinary(a); ok {
			out = append(out, a)
		}
	}
	return out
}

// serveAgentBinary hands the agent binary to an enrolled node (any agent
// token); ?arch= picks the architecture (default: this server's).
func (s *Server) serveAgentBinary(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.agentEndpoint(bearer(r))
	if !ok {
		http.Error(w, "invalid or missing token", http.StatusUnauthorized)
		return
	}
	agent.DefaultHub.SetVersion(ep.Name, r.Header.Get("X-Agent-Version"))
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = runtime.GOARCH
	}
	exe, ok := agentBinary(arch)
	if !ok {
		http.Error(w, "no agent binary for "+arch, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", `attachment; filename="ducktainer-agent"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, exe)
}

// agentVersion tells an agent which version this server runs and whether its
// host updates by itself; agents ask it periodically.
func (s *Server) agentVersion(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.agentEndpoint(bearer(r))
	if !ok {
		http.Error(w, "invalid or missing token", http.StatusUnauthorized)
		return
	}
	agent.DefaultHub.SetVersion(ep.Name, r.Header.Get("X-Agent-Version"))
	writeJSON(w, map[string]any{"version": s.Version, "auto": !ep.NoAutoUpdate})
}

// agentInstall returns the commands that enroll an agent host, for the
// admin UI. The token goes in a header (not in URLs that end up in proxy
// and access logs) and, with TLS, every download pins the server's key, so
// the very first contact is authenticated too.
func (s *Server) agentInstall(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.Cfg.Endpoint(r.PathValue("name"))
	if !ok || !ep.Agent() || ep.Token == "" {
		writeErr(w, herr(http.StatusNotFound, "not an agent host: %s", r.PathValue("name")))
		return
	}
	server := strings.TrimRight(r.URL.Query().Get("server"), "/")
	if !validServerURL(server) {
		writeErr(w, badRequest("invalid server URL: %s", server))
		return
	}
	fp, pin := s.agentPins()
	curl := "curl -fsSL"
	if pin != "" {
		curl += " -k --pinnedpubkey " + shellQuote(pin)
	}
	auth := "-H " + shellQuote("Authorization: Bearer "+ep.Token)
	flags := "--server " + shellQuote(server) + " --name " + shellQuote(ep.Name)
	if fp != "" {
		flags += " --fingerprint " + fp
	}
	cands, loopback := s.serverCandidates(r)
	writeJSON(w, map[string]any{
		"candidates":    cands,
		"loopback_only": loopback,
		"tls":           fp != "",
		"install": fmt.Sprintf("%s %s %s | sudo sh", curl, auth,
			shellQuote(server+"/api/agent/install.sh?server="+url.QueryEscape(server))),
		"manual": fmt.Sprintf("%s %s %s -o ducktainer-agent && chmod +x ducktainer-agent && sudo DUCKTAINER_AGENT_TOKEN=%s ./ducktainer-agent agent %s --install-service",
			curl, auth, shellQuote(server+"/api/agent/binary"), shellQuote(ep.Token), flags),
	})
}

// serveAgentInstallScript returns the installer run by `curl … | sudo sh`
// on a node; the token in the request decides which host it enrolls.
func (s *Server) serveAgentInstallScript(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	ep, ok := s.agentEndpoint(token)
	if !ok {
		http.Error(w, "invalid or missing token", http.StatusUnauthorized)
		return
	}

	scheme := "http"
	if s.Secure || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	serverURL := fmt.Sprintf("%s://%s", scheme, r.Host)
	if custom := strings.TrimRight(r.URL.Query().Get("server"), "/"); validServerURL(custom) {
		serverURL = custom
	}
	fp, pin := s.agentPins()
	curlTLS := ""
	if pin != "" {
		curlTLS = "-k --pinnedpubkey " + shellQuote(pin)
	}

	script := fmt.Sprintf(`#!/bin/sh
# ducktainer agent installer
set -eu

SERVER=%s
NAME=%s
FINGERPRINT=%s
TOKEN=%s

die() { echo "error: $*" >&2; echo "Nothing was installed." >&2; exit 1; }

echo "Installing ducktainer-agent as $NAME (server: $SERVER)"
[ "$(id -u)" = "0" ] || die "run it as root (| sudo sh): the agent is installed as a systemd service"
command -v curl >/dev/null 2>&1 || die "curl is required"

# Everything is checked before anything on this node is touched.
[ "$(uname -s)" = "Linux" ] || die "this installer is for Linux"
case "$(uname -m)" in
x86_64|amd64) ARCH=amd64 ;;
aarch64|arm64) ARCH=arm64 ;;
*) ARCH=$(uname -m) ;;
esac
case " %s " in
*" $ARCH "*) ;;
*) die "this server provides agent binaries for: %s; this node is linux/$ARCH" ;;
esac

BIN=/usr/local/bin/ducktainer-agent
rm -f "$BIN.new"
code=0
curl -fsSL %s -H "Authorization: Bearer $TOKEN" "$SERVER/api/agent/binary?arch=$ARCH" -o "$BIN.new" || code=$?
if [ "$code" != "0" ]; then
	rm -f "$BIN.new"
	case $code in
	6) die "cannot resolve the name in $SERVER" ;;
	7) die "cannot connect to $SERVER (connection refused). From this node 'localhost' is the node itself: use an address of the ducktainer server that this node can reach" ;;
	28) die "timed out connecting to $SERVER: a firewall may be dropping the connection" ;;
	35|52|56) die "$SERVER closed the connection. ducktainer drops addresses that are not in its allowlist: on the server run 'sudo ducktainer allow add <IP of this node>' and restart it, or start it with --agent-listen :9901 (agents only, token required) and use that port" ;;
	51|60|90) die "the server certificate does not match the pinned key: this is not the server the command was made for" ;;
	22) die "the server refused the token (is this host still registered in ducktainer?)" ;;
	*) die "could not download the agent from $SERVER (curl error $code)" ;;
	esac
fi
chmod 0755 "$BIN.new"
# a binary that cannot run here (usually a glibc that is too old) must not replace anything
if ! out=$("$BIN.new" version 2>&1); then
	rm -f "$BIN.new"
	echo "$out" >&2
	case $out in
	*GLIBC*|*libc.so*) die "this node's C library (glibc $(getconf GNU_LIBC_VERSION 2>/dev/null | cut -d' ' -f2)) is too old for the agent binary this server provides (glibc 2.34 or newer: RHEL 9, Debian 12, Ubuntu 22.04)" ;;
	esac
	die "the agent binary does not run on this node"
fi
echo "agent binary OK ($out)"

# The token never goes on a command line or in the unit: it is kept in a
# root-only file read by the service.
install -d -m 0700 /etc/ducktainer-agent
(umask 077 && printf 'DUCKTAINER_AGENT_TOKEN=%%s\n' "$TOKEN" > /etc/ducktainer-agent/agent.env)
mv -f "$BIN.new" "$BIN"

FLAGS="--server $SERVER --name $NAME"
[ -n "$FINGERPRINT" ] && FLAGS="$FLAGS --fingerprint $FINGERPRINT"

if ! command -v systemctl >/dev/null 2>&1 || [ ! -d /run/systemd/system ]; then
	echo "systemd not found; start the agent with:"
	echo "  env \$(cat /etc/ducktainer-agent/agent.env) $BIN agent $FLAGS"
	exit 0
fi

cat > /etc/systemd/system/ducktainer-agent.service <<UNIT
[Unit]
Description=ducktainer agent
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
EnvironmentFile=/etc/ducktainer-agent/agent.env
ExecStart=$BIN agent $FLAGS
Restart=always
RestartSec=5s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable ducktainer-agent >/dev/null 2>&1
systemctl restart ducktainer-agent
echo "ducktainer-agent is running: systemctl status ducktainer-agent"
`, shellQuote(serverURL), shellQuote(ep.Name), shellQuote(fp), shellQuote(token), strings.Join(agentArchs(), " "), strings.Join(agentArchs(), ", "), curlTLS)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(script))
}

func (s *Server) handleAgentTunnel(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.Header.Get("X-Agent-Name")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		http.Error(w, "missing agent name", http.StatusBadRequest)
		return
	}

	token := bearer(r)
	if token == "" {
		token = r.URL.Query().Get("token") // agents from earlier versions
	}

	// Only the host's own token opens its tunnel. Hosts (and their tokens)
	// are created from the UI or with `ducktainer endpoint add NAME agent://`.
	ep, authorized := s.agentEndpoint(token)
	if !authorized || ep.Name != name {
		log.Printf("[agent] unauthorized connection attempt for %q from %s", name, r.RemoteAddr)
		http.Error(w, "unauthorized: invalid agent token", http.StatusUnauthorized)
		return
	}

	ws, err := agentUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[agent] upgrade failed for %q: %v", name, err)
		return
	}
	defer ws.Close()

	wsConn := agent.NewWSConn(ws)
	yamuxConf := yamux.DefaultConfig()
	yamuxConf.EnableKeepAlive = true
	yamuxConf.KeepAliveInterval = 15 * time.Second
	yamuxConf.LogOutput = log.Writer()

	session, err := yamux.Server(wsConn, yamuxConf)
	if err != nil {
		log.Printf("[agent] yamux server error for %q: %v", name, err)
		return
	}
	defer session.Close()

	agent.DefaultHub.Register(name, session)
	agent.DefaultHub.SetAddr(name, access.RemoteIP(r, false).String())
	agent.DefaultHub.SetVersion(name, r.Header.Get("X-Agent-Version"))
	s.dropClient(name)
	log.Printf("[agent] node %q connected from %s ✓", name, r.RemoteAddr)

	<-session.CloseChan()

	agent.DefaultHub.Unregister(name, session)
	s.dropClient(name)
	log.Printf("[agent] node %q disconnected", name)
}

// serverCandidate is an address of this server that a node may be able to reach.
type serverCandidate struct {
	URL   string `json:"url"`
	Label string `json:"label"`
}

// serverCandidates lists the addresses to offer for `--server`: the
// interfaces ducktainer is listening on (and the name the browser used, when
// it is not loopback). loopbackOnly is true when it only listens on loopback,
// which no node can reach.
func (s *Server) serverCandidates(r *http.Request) (cands []serverCandidate, loopbackOnly bool) {
	scheme := "http"
	if s.Secure || r.TLS != nil {
		scheme = "https"
	}
	cands = []serverCandidate{}
	seen := map[string]bool{}
	add := func(hostport, label string) {
		u := scheme + "://" + hostport
		if !seen[u] {
			seen[u] = true
			cands = append(cands, serverCandidate{URL: u, Label: label})
		}
	}
	notLoopback := func(host string) bool {
		if ip := net.ParseIP(host); ip != nil {
			return !ip.IsLoopback()
		}
		return host != "localhost"
	}
	if host, _, err := net.SplitHostPort(r.Host); err == nil && notLoopback(host) {
		add(r.Host, "as opened in the browser")
	}
	listen := func(addr, what string) (reachable bool) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return false
		}
		if host != "" && host != "0.0.0.0" && host != "::" {
			if !notLoopback(host) {
				return false
			}
			add(net.JoinHostPort(host, port), what)
			return true
		}
		ifs, _ := net.Interfaces()
		for _, ifc := range ifs {
			if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || virtualIface(ifc.Name) {
				continue
			}
			addrs, _ := ifc.Addrs()
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
					add(net.JoinHostPort(ipn.IP.String(), port), what+" · "+ifc.Name)
				}
			}
		}
		return true
	}
	main := listen(s.Listen, "ducktainer")
	agents := s.AgentListen != "" && listen(s.AgentListen, "agents only")
	return cands, !main && !agents
}

// virtualIface skips the bridges and virtual links containers create.
func virtualIface(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "lo"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
