// ducktainer: agentless Docker administration in a single binary.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"ducktainer/internal/access"
	"ducktainer/internal/agent"
	"ducktainer/internal/auth"
	"ducktainer/internal/cli"
	"ducktainer/internal/compose"
	"ducktainer/internal/config"
	"ducktainer/internal/docker"
	"ducktainer/internal/dockersock"
	"ducktainer/internal/engine"
	"ducktainer/internal/i18n"
	"ducktainer/internal/proxy"
	"ducktainer/internal/server"
	"ducktainer/internal/setup"
	"ducktainer/internal/shortcuts"
	"ducktainer/internal/tlsutil"
	"ducktainer/packaging"
	"ducktainer/web"
)

var version = "dev"

const usage = `ducktainer — lightweight Docker management

Usage:
  ducktainer serve      [--listen 127.0.0.1:9900] [--allow IP,CIDR,host] [--tls auto|on|off]
                     [--expose :2376 [--expose-read-only] [--expose-no-exec]]
                     [--agent-listen :9901] [--tunnel-ports 41000-41099]
                       Web UI + API. With --expose it also publishes the Docker API
                       (filtered by allowlist + token) for other ducktainer instances or the docker CLI.
  ducktainer expose     [--listen :2376] [--allow ...] [--read-only] [--no-exec]
                       Only publishes the Docker API, protected (no UI).
  ducktainer agent      --server https://host:9900 [--name <name>] [--fingerprint <sha256>]
                     [--socket /var/run/docker.sock] [--insecure]   (token: DUCKTAINER_AGENT_TOKEN)
                     [--install-service]
                       Connects this node outbound to the central ducktainer server (ideal behind NAT/firewall).
  ducktainer ui                Opens the UI locally (127.0.0.1) and launches the browser.

  ducktainer ps    [-e endpoint]                         Containers grouped by stack
  ducktainer logs  [-e endpoint] [-f] [-n 200] <container|stack>
  ducktainer sh    [-e endpoint] [-u user] <container> [command...]
                                                      Interactive shell (picks bash/ash/sh)

  ducktainer auth show | set … | test <user>             Sign-in with system users (PAM) and roles by group
  ducktainer token [--rotate]                            Show/rotate the access token (emergency/API)
  ducktainer allow ls | add <IP|CIDR|host>... | rm <entry>...
  ducktainer endpoint ls | add <name> <url> [--token T] [--tls] [--fingerprint F] [--key file] | rm <name>
                       url: unix:///var/run/docker.sock · ssh://user@host[:port] · tcp://host:2376 · agent://name
  ducktainer registry ls | add <name> <server> <user> [--hosts h1,h2] (password on stdin) | rm <name>
  ducktainer docker-env <endpoint>                       Variables to use the docker CLI against an endpoint
  ducktainer install-service [serve flags]               Install a systemd service (root)
  ducktainer setup [--socket PATH] [--dry-run]           Adapt the service to the Docker installation (group,
                                                      rootless or root) based on the detected socket
  ducktainer engine install [--mode wsl|native] | status
                                                      Windows: install Docker without Docker Desktop
                                                      (wsl: Linux containers in WSL 2 · native: Windows containers)
  ducktainer version

Data and configuration: %s  (change with DUCKTAINER_HOME)
`

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	config.PAMAvailable = auth.Available
	config.DefaultAdminGroups = auth.DefaultAdminGroups()
	var cmd string
	var args []string
	if len(os.Args) < 2 {
		if strings.HasSuffix(filepath.Base(os.Args[0]), "agent") {
			cmd = "agent"
			args = nil
		} else {
			i18n.Fprintf(os.Stderr, usage, config.Dir())
			os.Exit(2)
		}
	} else {
		cmd, args = os.Args[1], os.Args[2:]
		if strings.HasSuffix(filepath.Base(os.Args[0]), "agent") && strings.HasPrefix(cmd, "-") {
			cmd = "agent"
			args = os.Args[1:]
		}
	}
	var err error
	switch cmd {
	case "serve":
		if runService(args) {
			return
		}
		err = cmdServe(args, false)
	case "ui":
		err = cmdServe(args, true)
	case "agent":
		err = cmdAgent(args)
	case "expose":
		err = cmdExpose(args)
	case "ps":
		err = withClient("ps", args, func(ctx context.Context, c *docker.Client, rest []string) error { return cli.PS(ctx, c) })
	case "logs":
		err = cmdLogs(args)
	case "sh", "exec":
		err = cmdShell(args)
	case "token":
		err = cmdToken(args)
	case "allow":
		err = cmdAllow(args)
	case "endpoint", "endpoints":
		err = cmdEndpoint(args)
	case "registry", "registries":
		err = cmdRegistry(args)
	case "docker-env":
		err = cmdDockerEnv(args)
	case "install-service":
		err = cmdInstall(args)
	case "setup":
		err = cmdSetup(args)
	case "engine":
		err = cmdEngine(args)
	case "pam-helper":
		err = cmdPAMHelper(args)
	case "auth":
		err = cmdAuth(args)
	case "version", "--version", "-v":
		fmt.Println("ducktainer", version)
	case "help", "-h", "--help":
		i18n.Printf(usage, config.Dir())
	default:
		i18n.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		i18n.Fprintf(os.Stderr, usage, config.Dir())
		os.Exit(2)
	}
	if err != nil {
		var ee interface{ ExitCode() int }
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "error:", i18n.Render(err, i18n.Env()))
		os.Exit(1)
	}
}

// newFlagSet is flag.NewFlagSet with -h output in the CLI language (the
// flag package prints "Usage of" and "(default …)" in English).
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() {
		out := fs.Output()
		i18n.Fprintf(out, "Usage of %s:\n", name)
		fs.VisitAll(func(f *flag.Flag) {
			var b strings.Builder
			fmt.Fprintf(&b, "  -%s", f.Name)
			kind, usage := flag.UnquoteUsage(f)
			if kind != "" {
				b.WriteString(" ")
				b.WriteString(kind)
			}
			if b.Len() <= 4 {
				b.WriteString("\t")
			} else {
				b.WriteString("\n    \t")
			}
			b.WriteString(strings.ReplaceAll(usage, "\n", "\n    \t"))
			if d := f.DefValue; d != "" && d != "false" && d != "0" {
				if kind == "string" {
					b.WriteString(i18n.Sprintf(" (default %q)", d))
				} else {
					b.WriteString(i18n.Sprintf(" (default %v)", d))
				}
			}
			fmt.Fprintln(out, b.String())
		})
	}
	return fs
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*l = append(*l, p)
		}
	}
	return nil
}

type listenOpts struct {
	allow      listFlag
	tlsMode    string
	cert, key  string
	trustProxy bool
}

func (o *listenOpts) register(fs *flag.FlagSet) {
	fs.Var(&o.allow, "allow", i18n.Sprintf("allowed IPs, CIDRs or hostnames (repeatable or comma-separated). Loopback is always allowed"))
	fs.StringVar(&o.tlsMode, "tls", "auto", i18n.Sprintf("auto (TLS when listening outside loopback), on, off"))
	fs.StringVar(&o.cert, "cert", "", i18n.Sprintf("TLS certificate (self-signed by default)"))
	fs.StringVar(&o.key, "key", "", i18n.Sprintf("TLS key"))
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (o *listenOpts) tlsConfig(addr string) (*tls.Config, string, error) {
	on := o.tlsMode == "on" || (o.tlsMode == "auto" && !isLoopbackAddr(addr))
	if !on {
		return nil, "", nil
	}
	return tlsutil.Load(config.Dir(), o.cert, o.key)
}

func listen(addr string, acl *access.List, tc *tls.Config) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ln = acl.Listener(ln)
	if tc != nil {
		ln = tls.NewListener(ln, tc)
	}
	return ln, nil
}

func localClient(cfg *config.Config, host string) (*docker.Client, error) {
	ep := config.Endpoint{Name: "local", URL: host}
	if host == "" {
		if e, ok := cfg.Endpoint("local"); ok && !strings.HasPrefix(e.URL, "tcp") {
			ep = e
		} else {
			ep.URL = "unix://" + dockersock.Path()
		}
	}
	return docker.New(ep)
}

func cmdServe(args []string, openBrowser bool) error {
	fs := newFlagSet("serve")
	var o listenOpts
	o.register(fs)
	addr := fs.String("listen", "", i18n.Sprintf("listen address (default 127.0.0.1:9900, or 0.0.0.0:9900 when --allow is used)"))
	expose := fs.String("expose", "", i18n.Sprintf("also publish the Docker API on this address (e.g. :2376)"))
	exposeRO := fs.Bool("expose-read-only", false, i18n.Sprintf("published API is read-only"))
	exposeNoExec := fs.Bool("expose-no-exec", false, i18n.Sprintf("block exec/attach on the published API"))
	agentListen := fs.String("agent-listen", "", i18n.Sprintf("dedicated listen address for agents without IP allowlist (e.g. :9901)"))
	tunnelPorts := fs.String("tunnel-ports", "41000-41099", i18n.Sprintf("port range for ephemeral tunnels to container ports"))
	fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *addr == "" {
		*addr = "127.0.0.1:9900"
		if len(o.allow) > 0 || len(cfg.Allow) > 0 {
			*addr = "0.0.0.0:9900"
		}
	}
	server.ExtraAllow = o.allow
	acl, err := access.New(append(append([]string(nil), o.allow...), cfg.Allow...))
	if err != nil {
		return err
	}
	tc, fp, err := o.tlsConfig(*addr)
	if err != nil {
		return err
	}
	srv := server.New(cfg, acl, web.FS())
	cfg.Watch(srv.ReloadAccess)
	srv.Secure = tc != nil
	if tc != nil {
		srv.TLSCert = tc.Certificates[0].Certificate[0]
	}
	srv.Version = version
	srv.Listen, srv.Expose, srv.TunnelPorts = *addr, *expose, *tunnelPorts
	srv.StartTunnels()
	srv.StartExposures()
	defer srv.StopExposures()
	srv.StartShortcuts()
	srv.StartAlerts()
	scheme := "http"
	if tc != nil {
		scheme = "https"
	}
	uiURL := fmt.Sprintf("%s://%s", scheme, displayAddr(*addr))
	ln, err := listen(*addr, acl, tc)
	if err != nil {
		// `ui` with ducktainer already running there (the Windows service,
		// the systemd one): just open it
		if openBrowser && runningAt(uiURL) {
			i18n.Printf("ducktainer is already running at %s\n", uiURL)
			return openURL(uiURL)
		}
		return err
	}
	fmt.Printf("\n  ducktainer %s\n\n", version)
	fmt.Printf("  UI         %s\n", uiURL)
	snap := cfg.Snapshot()
	if snap.Auth.PAM {
		i18n.Printf("  Users      system (%s) · admin: %s · read-only: %s\n", i18n.Sprintf(auth.Mode()), strings.Join(snap.Auth.AdminGroups, ","), strings.Join(snap.Auth.ViewerGroups, ","))
	}
	if snap.Auth.TokenLogin {
		fmt.Printf("  Token      %s\n", tokenLine(cfg))
	}
	fmt.Printf("  Allowlist  loopback %s\n", strings.Join(acl.Entries(), " "))
	if fp != "" {
		fmt.Printf("  TLS sha256 %s\n", fp)
	}
	i18n.Printf("  Data       %s\n\n", config.Dir())

	errc := make(chan error, 2)
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 15 * time.Second}
	go func() { errc <- hs.Serve(ln) }()

	if *expose != "" {
		c, err := localClient(cfg, "")
		if err != nil {
			return err
		}
		etc, efp, err := o.tlsConfig(*expose)
		if err != nil {
			return err
		}
		eln, err := listen(*expose, acl, etc)
		if err != nil {
			return err
		}
		ph := proxy.Handler(c, proxy.Options{Check: cfg.CheckToken, ReadOnly: *exposeRO, NoExec: *exposeNoExec})
		i18n.Printf("  Docker API exposed on %s (token required", *expose)
		if efp != "" {
			fmt.Printf(", TLS sha256 %s", efp)
		}
		fmt.Println(")")
		go func() { errc <- (&http.Server{Handler: ph, ReadHeaderTimeout: 15 * time.Second}).Serve(eln) }()
	}
	if *agentListen != "" {
		srv.AgentListen = *agentListen
		atc, afp, err := o.tlsConfig(*agentListen)
		if err != nil {
			return err
		}
		aln, err := net.Listen("tcp", *agentListen)
		if err != nil {
			return err
		}
		if atc != nil {
			aln = tls.NewListener(aln, atc)
			if srv.TLSCert == nil {
				srv.TLSCert = atc.Certificates[0].Certificate[0]
			}
		}
		agentHs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 15 * time.Second}
		go func() { errc <- agentHs.Serve(aln) }()
		i18n.Printf("  Agents     listening on %s (token required, no IP allowlist", *agentListen)
		if afp != "" {
			fmt.Printf(", TLS sha256 %s", afp)
		}
		fmt.Println(")")
	}
	if openBrowser {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openURL(uiURL)
		}()
	}
	return waitSignal(errc)
}

// openURL opens url in the default browser.
func openURL(url string) error {
	if runtime.GOOS == "windows" {
		return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
	}
	var err error
	for _, b := range []string{"xdg-open", "open"} {
		if err = exec.Command(b, url).Start(); err == nil {
			return nil
		}
	}
	return err
}

// runningAt reports whether a ducktainer answers at url.
func runningAt(url string) bool {
	c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := c.Get(url + "/api/auth")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var info struct {
		Version *string `json:"version"`
	}
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&info) == nil && info.Version != nil
}

func displayAddr(addr string) string {
	host, port, _ := net.SplitHostPort(addr)
	if host == "" || host == "0.0.0.0" || host == "::" {
		if h, err := os.Hostname(); err == nil {
			host = h
		}
	}
	return net.JoinHostPort(host, port)
}

func waitSignal(errc chan error) error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errc:
		return err
	case <-sig:
		fmt.Println("\ndetenido")
		return nil
	}
}

func cmdAgent(args []string) error {
	fs := newFlagSet("agent")
	serverURL := fs.String("server", "", i18n.Sprintf("ducktainer server URL (e.g. https://ducktainer.example.com:9900)"))
	token := fs.String("token", "", i18n.Sprintf("the host's agent token (or DUCKTAINER_AGENT_TOKEN)"))
	name := fs.String("name", "", i18n.Sprintf("node name (defaults to hostname)"))
	socket := fs.String("socket", "", i18n.Sprintf("local Docker socket path (default /var/run/docker.sock)"))
	insecure := fs.Bool("insecure", false, i18n.Sprintf("skip TLS certificate verification (useful for self-signed certificates)"))
	fp := fs.String("fingerprint", "", i18n.Sprintf("expected sha256 fingerprint of the server certificate"))
	tokenFile := fs.String("token-file", "", i18n.Sprintf("file holding the agent token (what the Windows service reads)"))
	installService := fs.Bool("install-service", false, i18n.Sprintf("install and start as a service (systemd on Linux, Windows service on Windows)"))
	fs.Parse(args)
	if *token == "" {
		// preferred: keeps the token out of `ps` and of the unit file
		*token = os.Getenv("DUCKTAINER_AGENT_TOKEN")
	}
	if *token == "" && *tokenFile != "" {
		t, err := agent.ReadTokenFile(*tokenFile)
		if err != nil {
			return err
		}
		*token = t
	}

	cfg := agent.Config{
		Server:      *serverURL,
		Token:       *token,
		Name:        *name,
		Socket:      *socket,
		Insecure:    *insecure,
		Fingerprint: *fp,
		Version:     version,
	}

	if *installService {
		return agent.InstallService(cfg)
	}
	if runAgentService(cfg) { // started by the Windows Service Control Manager
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return agent.Run(ctx, cfg)
}

func cmdExpose(args []string) error {
	fs := newFlagSet("expose")
	var o listenOpts
	o.register(fs)
	addr := fs.String("listen", ":2376", i18n.Sprintf("listen address"))
	host := fs.String("host", "", i18n.Sprintf("local daemon (default: the local endpoint / socket)"))
	ro := fs.Bool("read-only", false, i18n.Sprintf("allow reads only (GET)"))
	noExec := fs.Bool("no-exec", false, i18n.Sprintf("block exec/attach"))
	fs.Parse(args)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	acl, err := access.New(append(append([]string(nil), o.allow...), cfg.Allow...))
	if err != nil {
		return err
	}
	if len(acl.Entries()) == 0 {
		i18n.Fprintf(os.Stderr, "warning: empty allowlist, only loopback can connect (use --allow)\n")
	}
	c, err := localClient(cfg, *host)
	if err != nil {
		return err
	}
	if err := c.Ping(context.Background()); err != nil {
		return i18n.Errorf("cannot talk to the local daemon: %v", err)
	}
	tc, fp, err := o.tlsConfig(*addr)
	if err != nil {
		return err
	}
	ln, err := listen(*addr, acl, tc)
	if err != nil {
		return err
	}
	h := proxy.Handler(c, proxy.Options{Check: cfg.CheckToken, ReadOnly: *ro, NoExec: *noExec})
	i18n.Printf("\n  Docker API exposed on %s\n", *addr)
	fmt.Printf("  Allowlist  loopback %s\n", strings.Join(acl.Entries(), " "))
	fmt.Printf("  Token      %s\n", tokenLine(cfg))
	tlsFlag := ""
	if fp != "" {
		fmt.Printf("  TLS sha256 %s\n", fp)
		tlsFlag = " --tls --fingerprint " + fp
	}
	i18n.Printf("\n  From another ducktainer:\n    ducktainer endpoint add %s tcp://%s --token %s%s\n\n", hostname(), displayAddr(*addr), shownToken(cfg), tlsFlag)
	errc := make(chan error, 1)
	go func() { errc <- (&http.Server{Handler: h, ReadHeaderTimeout: 15 * time.Second}).Serve(ln) }()
	return waitSignal(errc)
}

func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "remoto"
	}
	return strings.Split(h, ".")[0]
}

// withClient parses -e and runs fn with the endpoint's client.
func withClient(name string, args []string, fn func(ctx context.Context, c *docker.Client, rest []string) error, extra ...func(*flag.FlagSet)) error {
	fs := newFlagSet(name)
	ep := fs.String("e", "local", i18n.Sprintf("host to use (see: ducktainer endpoint ls)"))
	for _, f := range extra {
		f(fs)
	}
	fs.Parse(args)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	e, ok := cfg.Endpoint(*ep)
	if !ok {
		return i18n.Errorf("unknown endpoint %q (see `ducktainer endpoint ls`)", *ep)
	}
	c, err := docker.New(e)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return fn(ctx, c, fs.Args())
}

func cmdLogs(args []string) error {
	var follow bool
	var tail string
	return withClient("logs", args, func(ctx context.Context, c *docker.Client, rest []string) error {
		if len(rest) != 1 {
			return i18n.Errorf("usage: ducktainer logs [-f] [-n 200] <container|stack>")
		}
		return cli.Logs(ctx, c, rest[0], follow, tail)
	}, func(fs *flag.FlagSet) {
		fs.BoolVar(&follow, "f", false, i18n.Sprintf("follow"))
		fs.StringVar(&tail, "n", "200", i18n.Sprintf("initial lines (all for everything)"))
	})
}

func cmdShell(args []string) error {
	var user string
	return withClient("sh", args, func(ctx context.Context, c *docker.Client, rest []string) error {
		if len(rest) < 1 {
			return i18n.Errorf("usage: ducktainer sh [-u user] <container> [command...]")
		}
		// the shell must not be killed by ctrl-c: it belongs to the remote tty
		return cli.Shell(context.Background(), c, rest[0], user, rest[1:])
	}, func(fs *flag.FlagSet) { fs.StringVar(&user, "u", "", i18n.Sprintf("user")) })
}

func cmdToken(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "--rotate" {
		t := config.NewToken()
		if err := cfg.Update(func(c *config.Config) error { c.SetToken(t); return nil }); err != nil {
			return err
		}
		i18n.Fprintf(os.Stderr, "token rotated; running services pick it up automatically\n")
		fmt.Println(t)
		return nil
	}
	if t := cfg.FreshToken(); t != "" {
		fmt.Println(t)
		return nil
	}
	return i18n.Errorf("the token is stored hashed and cannot be shown again: `ducktainer token --rotate` creates and prints a new one")
}

// shownToken is the access token when this process created it, else a
// placeholder (only its hash is stored).
func shownToken(cfg *config.Config) string {
	if t := cfg.FreshToken(); t != "" {
		return t
	}
	return "<token>"
}

func tokenLine(cfg *config.Config) string {
	if t := cfg.FreshToken(); t != "" {
		return t + i18n.Sprintf("  (shown once: only its hash is stored)")
	}
	return i18n.Sprintf("(stored hashed; `ducktainer token --rotate` prints a new one)")
}

func cmdAllow(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "ls" {
		i18n.Printf("loopback (always)\n")
		for _, a := range cfg.Allow {
			fmt.Println(a)
		}
		return nil
	}
	switch args[0] {
	case "add":
		if _, err := access.New(args[1:]); err != nil {
			return err
		}
		return cfg.Update(func(c *config.Config) error { c.Allow = appendUnique(c.Allow, args[1:]...); return nil })
	case "rm":
		return cfg.Update(func(c *config.Config) error {
			out := c.Allow[:0]
			for _, a := range c.Allow {
				if !contains(args[1:], a) {
					out = append(out, a)
				}
			}
			c.Allow = out
			return nil
		})
	}
	return i18n.Errorf("usage: ducktainer allow ls | add ... | rm ...")
}

func appendUnique(s []string, v ...string) []string {
	for _, x := range v {
		if !contains(s, x) {
			s = append(s, x)
		}
	}
	return s
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func cmdEndpoint(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "ls" {
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		i18n.Fprintf(tw, "NAME\tURL\tSTATUS\n")
		for _, e := range cfg.Snapshot().Endpoints {
			status := "ok"
			c, err := docker.New(e)
			if err == nil {
				err = c.Ping(context.Background())
			}
			if err != nil {
				status = "error: " + i18n.Render(err, i18n.Env())
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Name, e.URL, status)
		}
		return tw.Flush()
	}
	switch args[0] {
	case "add":
		fs := newFlagSet("endpoint add")
		token := fs.String("token", "", i18n.Sprintf("token of the remote `ducktainer expose`"))
		useTLS := fs.Bool("tls", false, i18n.Sprintf("use TLS"))
		fp := fs.String("fingerprint", "", i18n.Sprintf("sha256 fingerprint of the remote certificate"))
		key := fs.String("key", "", i18n.Sprintf("SSH key"))
		if len(args) < 3 {
			return i18n.Errorf("usage: ducktainer endpoint add <name> <url> [--token T] [--tls] [--fingerprint F] [--key file]")
		}
		fs.Parse(args[3:])
		ep := config.Endpoint{Name: args[1], URL: args[2], Token: *token, TLS: *useTLS || *fp != "", Fingerprint: *fp, SSHKey: *key}
		if ep.Agent() {
			if ep.URL == "agent://" || ep.URL == "agent:" {
				ep.URL = "agent://" + ep.Name
			}
			if ep.Token == "" {
				ep.Token = config.NewToken()
			}
			i18n.Printf("agent token for %s: %s\n  (Settings → Hosts → Copy agent install command gives the full, pinned command)\n", ep.Name, ep.Token)
		}
		pinned, err := docker.Secure(context.Background(), ep)
		if err != nil {
			return err
		}
		if pinned.Fingerprint != ep.Fingerprint {
			i18n.Fprintf(os.Stderr, "pinned TLS certificate sha256 %s\n", pinned.Fingerprint)
		}
		ep = pinned
		c, err := docker.New(ep)
		if err != nil {
			return err
		}
		if err := c.Ping(context.Background()); err != nil {
			i18n.Fprintf(os.Stderr, "warning: could not connect right now: %v\n", err)
		} else {
			i18n.Fprintf(os.Stderr, "connection verified ✓\n")
		}
		return cfg.Update(func(c *config.Config) error {
			for i, e := range c.Endpoints {
				if e.Name == ep.Name {
					c.Endpoints[i] = ep
					return nil
				}
			}
			c.Endpoints = append(c.Endpoints, ep)
			return nil
		})
	case "rm":
		if len(args) < 2 {
			return i18n.Errorf("usage: ducktainer endpoint rm <name>")
		}
		return cfg.Update(func(c *config.Config) error {
			out := c.Endpoints[:0]
			for _, e := range c.Endpoints {
				if e.Name != args[1] {
					out = append(out, e)
				}
			}
			c.Endpoints = out
			return nil
		})
	}
	return i18n.Errorf("usage: ducktainer endpoint ls | add | rm")
}

func cmdRegistry(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "ls" {
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		i18n.Fprintf(tw, "NAME\tSERVER\tUSER\n")
		for _, r := range cfg.Registry {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Name, r.Server, r.Username)
		}
		return tw.Flush()
	}
	switch args[0] {
	case "add":
		if len(args) < 4 {
			return i18n.Errorf("usage: echo $PASS | ducktainer registry add <name> <server> <user> [--hosts h1,h2]")
		}
		fs := newFlagSet("registry add")
		var hosts listFlag
		fs.Var(&hosts, "hosts", i18n.Sprintf("only hand these credentials to these hosts (default: all)"))
		fs.Parse(args[4:])
		i18n.Fprintf(os.Stderr, "password/token: ")
		pass, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		reg := config.Registry{Name: args[1], Server: args[2], Username: args[3], Password: strings.TrimSpace(pass), Endpoints: hosts}
		return cfg.Update(func(c *config.Config) error {
			for i, r := range c.Registry {
				if r.Name == reg.Name {
					c.Registry[i] = reg
					return nil
				}
			}
			c.Registry = append(c.Registry, reg)
			return nil
		})
	case "rm":
		if len(args) < 2 {
			return i18n.Errorf("usage: ducktainer registry rm <name>")
		}
		return cfg.Update(func(c *config.Config) error {
			out := c.Registry[:0]
			for _, r := range c.Registry {
				if r.Name != args[1] {
					out = append(out, r)
				}
			}
			c.Registry = out
			return nil
		})
	}
	return i18n.Errorf("usage: ducktainer registry ls | add | rm")
}

// cmdDockerEnv prints how to point the docker CLI at an endpoint.
func cmdDockerEnv(args []string) error {
	if len(args) != 1 {
		return i18n.Errorf("usage: ducktainer docker-env <endpoint>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	e, ok := cfg.Endpoint(args[0])
	if !ok {
		return i18n.Errorf("unknown endpoint %q", args[0])
	}
	fmt.Printf("export DOCKER_HOST=%s\n", e.URL)
	dir := filepath.Join(config.Dir(), "docker-config", e.Name)
	if e.Token != "" || e.TLS {
		os.MkdirAll(dir, 0o700)
	}
	if e.Token != "" {
		data := fmt.Sprintf(`{"HttpHeaders":{"Authorization":"Bearer %s"}}`, e.Token)
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0o600); err != nil {
			return err
		}
		fmt.Printf("export DOCKER_CONFIG=%s\n", dir)
	}
	if e.TLS {
		vars, err := compose.TLSEnv(context.Background(), e, dir)
		if err != nil {
			return err
		}
		for _, v := range vars {
			fmt.Println("export " + v)
		}
	}
	fmt.Println("# eval \"$(ducktainer docker-env " + e.Name + ")\"")
	return nil
}

func cmdInstall(args []string) error {
	if runtime.GOOS == "windows" {
		return i18n.Errorf("on Windows the service is installed by the MSI package (ducktainer-<version>-x64.msi, on the releases page)")
	}
	if runtime.GOOS != "linux" {
		return i18n.Errorf("install-service is only available on Linux (systemd)")
	}
	if os.Geteuid() != 0 {
		return i18n.Errorf("run as root: sudo ducktainer install-service [serve flags]")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// same layout as the rpm/deb packages
	const bin = "/usr/bin/ducktainer"
	if self != bin {
		data, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if err := os.WriteFile(bin+".new", data, 0o755); err != nil {
			return err
		}
		if err := os.Rename(bin+".new", bin); err != nil {
			return err
		}
	}
	write := func(src, dst string, keep bool) error {
		if keep {
			if _, err := os.Stat(dst); err == nil {
				return nil
			}
		}
		data, err := fs.ReadFile(packaging.Files, src)
		if err != nil {
			return err
		}
		os.MkdirAll(filepath.Dir(dst), 0o755)
		return os.WriteFile(dst, data, 0o644)
	}
	for _, u := range []string{"ducktainer.service", "ducktainer-pam.socket", "ducktainer-pam@.service"} {
		if err := write("systemd/"+u, "/usr/lib/systemd/system/"+u, false); err != nil {
			return err
		}
		// copies from earlier versions in /etc would shadow the ones above
		os.Remove("/etc/systemd/system/" + u)
	}
	// the old location of the binary
	os.Remove("/usr/local/bin/ducktainer")
	pamSrc := "pam/ducktainer.debian"
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	switch {
	case exists("/etc/pam.d/password-auth"):
		pamSrc = "pam/ducktainer.redhat"
	case exists("/etc/pam.d/common-auth"):
	case exists("/etc/pam.d/system-auth"): // Arch
		pamSrc = "pam/ducktainer.arch"
	}
	if err := write(pamSrc, "/etc/pam.d/ducktainer", true); err != nil {
		return err
	}
	if _, err := os.Stat("/etc/ducktainer/ducktainer.env"); os.IsNotExist(err) {
		if err := write("ducktainer.env", "/etc/ducktainer/ducktainer.env", true); err != nil {
			return err
		}
		if len(args) > 0 {
			env, err := os.OpenFile("/etc/ducktainer/ducktainer.env", os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(env, "DUCKTAINER_OPTS=%q\n", strings.Join(args, " "))
			env.Close()
			if err != nil {
				return err
			}
		}
	}
	script, err := fs.ReadFile(packaging.Files, "scripts/postinstall.sh")
	if err != nil {
		return err
	}
	cmd := exec.Command("/bin/sh", "-s", "1")
	cmd.Stdin = bytes.NewReader(script)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return i18n.Errorf("post-install: %v", err)
	}
	if !auth.Available {
		i18n.Printf("warning: this binary has no PAM support (built without -tags pam): only token sign-in will work.\n         Use the rpm/deb packages or build with `make build-pam`.\n")
	}
	return nil
}

func cmdPAMHelper(args []string) error {
	fs := newFlagSet("pam-helper")
	service := fs.String("service", "ducktainer", i18n.Sprintf("PAM service"))
	fs.Parse(args)
	return auth.ServeHelper(*service, os.Stdin, os.Stdout)
}

func cmdAuth(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	sub := "show"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "show":
		a := cfg.Snapshot().Auth
		i18n.Printf("system users (PAM):  %v   [PAM service %q · %s]\n", a.PAM, a.PAMService, i18n.Sprintf(auth.Mode()))
		i18n.Printf("token sign-in:       %v\n", a.TokenLogin)
		i18n.Printf("admin groups:        %s\n", strings.Join(a.AdminGroups, ", "))
		i18n.Printf("read-only groups:    %s\n", strings.Join(a.ViewerGroups, ", "))
		i18n.Printf("admin users:         %s\n", strings.Join(a.AdminUsers, ", "))
		i18n.Printf("read-only users:     %s\n", strings.Join(a.ViewerUsers, ", "))
		i18n.Printf("allow root:          %v\n", a.AllowRoot)
		i18n.Printf("session duration:    %dh\n", a.SessionHours)
		return nil
	case "set":
		fs := newFlagSet("auth set")
		pam := fs.String("pam", "", "on|off")
		tok := fs.String("token-login", "", "on|off")
		root := fs.String("allow-root", "", "on|off")
		var ag, vg, au, vu listFlag
		fs.Var(&ag, "admin-groups", i18n.Sprintf("admin groups (replaces the list)"))
		fs.Var(&vg, "viewer-groups", i18n.Sprintf("read-only groups"))
		fs.Var(&au, "admin-users", i18n.Sprintf("admin users"))
		fs.Var(&vu, "viewer-users", i18n.Sprintf("read-only users"))
		hours := fs.Int("session-hours", 0, i18n.Sprintf("session duration"))
		fs.Parse(args)
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		onoff := func(v string) bool { return v == "on" || v == "true" || v == "1" }
		err := cfg.Update(func(c *config.Config) error {
			a := c.Auth
			if set["pam"] {
				a.PAM = onoff(*pam)
			}
			if set["token-login"] {
				a.TokenLogin = onoff(*tok)
			}
			if set["allow-root"] {
				a.AllowRoot = onoff(*root)
			}
			if set["admin-groups"] {
				a.AdminGroups = ag
			}
			if set["viewer-groups"] {
				a.ViewerGroups = vg
			}
			if set["admin-users"] {
				a.AdminUsers = au
			}
			if set["viewer-users"] {
				a.ViewerUsers = vu
			}
			if *hours > 0 {
				a.SessionHours = *hours
			}
			if !a.PAM && !a.TokenLogin {
				return i18n.Errorf("keep at least one sign-in method enabled")
			}
			return nil
		})
		if err != nil {
			return err
		}
		return cmdAuth([]string{"show"})
	case "test":
		if len(args) != 1 {
			return i18n.Errorf("usage: ducktainer auth test <user>")
		}
		i18n.Fprintf(os.Stderr, "password for %s: ", args[0])
		var pass []byte
		if term.IsTerminal(int(os.Stdin.Fd())) {
			pass, err = term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
		} else {
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			pass = []byte(strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			return err
		}
		a := cfg.Snapshot().Auth
		i18n.Printf("method: %s\n", i18n.Sprintf(auth.Mode()))
		if err := auth.Login(a.PAMService, args[0], string(pass), ""); err != nil {
			return i18n.Errorf("authentication: %v", err)
		}
		i18n.Printf("password correct ✓\n")
		groups, _ := auth.Groups(args[0])
		i18n.Printf("groups: %s\n", strings.Join(groups, ", "))
		role, err := (auth.Policy{AdminGroups: a.AdminGroups, ViewerGroups: a.ViewerGroups, AdminUsers: a.AdminUsers, ViewerUsers: a.ViewerUsers, AllowRoot: a.AllowRoot}).Role(args[0])
		if err != nil {
			return err
		}
		i18n.Printf("ducktainer role: %s\n", role)
		return nil
	}
	return i18n.Errorf("usage: ducktainer auth show | set [--pam on|off] [--token-login on|off] [--admin-groups a,b] [--viewer-groups c] … | test <user>")
}

func cmdSetup(args []string) error {
	fs := newFlagSet("setup")
	socket := fs.String("socket", "", i18n.Sprintf("daemon socket (detected by default)"))
	dry := fs.Bool("dry-run", false, i18n.Sprintf("only show what would be done"))
	noRestart := fs.Bool("no-restart", false, i18n.Sprintf("do not restart ducktainer.service"))
	quiet := fs.Bool("quiet", false, i18n.Sprintf("less output"))
	fs.Parse(args)
	plan := setup.Detect(*socket)
	if !*quiet || *dry {
		fmt.Printf("socket:  %s", plan.Socket)
		if plan.Exists {
			fmt.Printf("  (%s:%s %04o, %s)", plan.Owner, plan.Group, plan.Perm, plan.Source)
		} else if plan.Source != "" {
			fmt.Printf("  (%s)", plan.Source)
		}
		i18n.Printf("\nmode:    %s\n", plan.Mode)
		for _, n := range plan.Notes {
			fmt.Println("  ·", n)
		}
	}
	if *dry {
		if c := plan.DropInContent(); c != "" {
			fmt.Printf("\n%s:\n%s", setup.DropIn, c)
		}
		return nil
	}
	if err := plan.Apply(!*noRestart); err != nil {
		return err
	}
	// point the "local" endpoint at the detected socket
	if plan.Socket != "" {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		err = cfg.Update(func(c *config.Config) error {
			for i, e := range c.Endpoints {
				if e.Name == "local" && strings.HasPrefix(e.URL, "unix://") {
					c.Endpoints[i].URL = "unix://" + plan.Socket
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	// start menu entries for containers (machines with a desktop)
	if ok, err := shortcuts.InstallSystem(); err != nil {
		i18n.Printf("warning: could not prepare the start menu: %v\n", err)
	} else if ok && !*quiet {
		i18n.Printf("start menu: containers can be added from Settings → Access → Start menu\n")
	}
	if !*quiet {
		if plan.Mode == setup.ModeNone {
			i18n.Printf("no changes: there is no Docker to configure\n")
		} else {
			i18n.Printf("service configured: %s\n", setup.DropIn)
		}
	}
	return nil
}

// cmdEngine installs Docker on Windows without Docker Desktop.
func cmdEngine(args []string) error {
	if len(args) == 0 || (args[0] != "install" && args[0] != "status" && args[0] != "relay") {
		return i18n.Errorf("usage: ducktainer engine install [--mode wsl|native] [--distro NAME] [--version X.Y.Z] [--sha256 HASH] [--no-keepalive] | status | relay [--distro NAME]")
	}
	fs := newFlagSet("engine")
	mode := fs.String("mode", "wsl", i18n.Sprintf("wsl: Linux containers in a WSL 2 distribution · native: Windows containers with dockerd.exe"))
	distro := fs.String("distro", engine.DefaultDistro, i18n.Sprintf("WSL distribution created for Docker"))
	ver := fs.String("version", "", i18n.Sprintf("Docker version for native mode (default: the latest)"))
	sum := fs.String("sha256", "", i18n.Sprintf("expected sha256 of the Docker zip (native mode)"))
	noKeep := fs.Bool("no-keepalive", false, i18n.Sprintf("do not keep the WSL distribution running at logon"))
	fs.Parse(args[1:])
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if args[0] == "status" {
		return engine.Status(ctx, os.Stdout, *distro)
	}
	if args[0] == "relay" {
		return engine.Relay(ctx, *distro)
	}
	res, err := engine.Install(ctx, engine.Options{Mode: *mode, Distro: *distro, Version: *ver, SHA256: *sum, NoKeepAlive: *noKeep, Out: os.Stdout})
	if err != nil {
		return err
	}
	if res.Reboot {
		i18n.Printf("Restart Windows and run `ducktainer engine install --mode %s` again to finish.\n", *mode)
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.Update(func(c *config.Config) error {
		for i, e := range c.Endpoints {
			if e.Name == res.Name {
				c.Endpoints[i].URL = res.URL
				return nil
			}
		}
		c.Endpoints = append(c.Endpoints, config.Endpoint{Name: res.Name, URL: res.URL})
		return nil
	}); err != nil {
		return err
	}
	i18n.Printf("Docker is ready: host %s (%s). Open the UI with `ducktainer ui`.\n", res.Name, res.URL)
	return nil
}
