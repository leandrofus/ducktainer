package server

import (
	"context"
	"ducktainer/internal/access"
	"ducktainer/internal/i18n"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/user"
	"runtime"
	"strings"
	"sync"
	"time"

	"ducktainer/internal/agent"
	"ducktainer/internal/auth"
	"ducktainer/internal/config"
	"ducktainer/internal/docker"
)

type endpointStatus struct {
	config.Endpoint
	Online  bool   `json:"online"`
	Error   string `json:"error,omitempty"`
	Version string `json:"version,omitempty"`
	OS      string `json:"os,omitempty"`
	Running int    `json:"running"`
	Total   int    `json:"total"`
	// AgentVersion is what the connected agent reported (agent hosts).
	AgentVersion string `json:"agent_version,omitempty"`
}

// listEndpoints returns the configured endpoints with live status; secrets
// are not sent back to the browser.
func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request) {
	eps := s.Cfg.Snapshot().Endpoints
	out := make([]endpointStatus, len(eps))
	var wg sync.WaitGroup
	for i, ep := range eps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := endpointStatus{Endpoint: ep}
			if ep.Agent() {
				st.AgentVersion = agent.DefaultHub.Version(ep.Name)
			}
			st.Token = ""
			if ep.Token != "" {
				st.Token = "••••"
			}
			c, err := s.Client(ep.Name)
			if err == nil {
				ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
				defer cancel()
				var info struct {
					ServerVersion     string
					OperatingSystem   string
					Containers        int
					ContainersRunning int
				}
				err = c.Get(ctx, "/info", nil, &info)
				st.Version, st.OS, st.Total, st.Running = info.ServerVersion, info.OperatingSystem, info.Containers, info.ContainersRunning
			}
			st.Online = err == nil
			if err != nil {
				st.Error = i18n.Render(err, reqLang(r))
			}
			out[i] = st
		}()
	}
	wg.Wait()
	writeJSON(w, out)
}

func (s *Server) saveEndpoint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		config.Endpoint
		Original string `json:"original"` // name being edited, if any
		Test     bool   `json:"test"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	ep := body.Endpoint
	ep.Name = strings.TrimSpace(ep.Name)
	ep.URL = strings.TrimSpace(ep.URL)
	if ep.Name == "" || strings.ContainsAny(ep.Name, "/ ") {
		writeErr(w, badRequest("invalid name"))
		return
	}
	if _, err := url.Parse(ep.URL); err != nil || (!strings.Contains(ep.URL, "://") && !strings.HasPrefix(ep.URL, "agent:")) {
		writeErr(w, badRequest("invalid URL: use unix:///path, ssh://user@host, tcp://host:port or agent://name"))
		return
	}
	if strings.HasPrefix(ep.URL, "agent://") || ep.URL == "agent" {
		if ep.URL == "agent" || ep.URL == "agent://" {
			ep.URL = "agent://" + ep.Name
		}
		if ep.Token == "" {
			ep.Token = config.NewToken()
		}
	}
	// keep the stored token when the UI sends back the mask
	if prev, ok := s.Cfg.Endpoint(body.Original); ok && (ep.Token == "••••") {
		ep.Token = prev.Token
	}
	ep, err := docker.Secure(r.Context(), ep)
	if err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	c, err := docker.New(ep)
	if err == nil && (!strings.HasPrefix(ep.URL, "agent://") || agent.DefaultHub.IsOnline(ep.Name)) {
		err = c.Ping(r.Context())
	}
	if err != nil && body.Test {
		writeErr(w, badRequest("could not connect: %v", err))
		return
	}
	err = s.Cfg.Update(func(cfg *config.Config) error {
		for i, e := range cfg.Endpoints {
			if e.Name == ep.Name && e.Name != body.Original {
				return badRequest("an endpoint with that name already exists")
			}
			_ = i
		}
		for i, e := range cfg.Endpoints {
			if e.Name == body.Original {
				cfg.Endpoints[i] = ep
				return nil
			}
		}
		cfg.Endpoints = append(cfg.Endpoints, ep)
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.dropClient(body.Original)
	s.dropClient(ep.Name)
	writeJSON(w, map[string]any{"ok": true, "fingerprint": ep.Fingerprint})
}

func (s *Server) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.Cfg.Update(func(cfg *config.Config) error {
		out := cfg.Endpoints[:0]
		for _, e := range cfg.Endpoints {
			if e.Name != name {
				out = append(out, e)
			}
		}
		if len(out) == 0 {
			return badRequest("at least one endpoint must remain")
		}
		cfg.Endpoints = out
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.dropClient(name)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) listRegistries(w http.ResponseWriter, r *http.Request) {
	regs := s.Cfg.Snapshot().Registry
	for i := range regs {
		if regs[i].Password != "" {
			regs[i].Password = "••••"
		}
	}
	if regs == nil {
		regs = []config.Registry{}
	}
	writeJSON(w, regs)
}

func (s *Server) saveRegistry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		config.Registry
		Original string `json:"original"`
		Endpoint string `json:"endpoint"` // used to verify credentials
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	reg := body.Registry
	reg.Name = strings.TrimSpace(reg.Name)
	if reg.Name == "" {
		reg.Name = reg.Server
	}
	if reg.Server == "" {
		reg.Server = "docker.io"
	}
	for _, prev := range s.Cfg.Snapshot().Registry {
		if prev.Name == body.Original && reg.Password == "••••" {
			reg.Password = prev.Password
		}
	}
	// Verify credentials against the daemon (POST /auth) when possible.
	if body.Endpoint != "" && reg.Username != "" {
		if c, err := s.Client(body.Endpoint); err == nil {
			server := reg.Server
			if server == "docker.io" {
				server = "https://index.docker.io/v1/"
			}
			if err := c.JSON(r.Context(), http.MethodPost, "/auth", nil, map[string]string{
				"username": reg.Username, "password": reg.Password, "serveraddress": server,
			}, nil); err != nil {
				writeErr(w, badRequest("credentials rejected: %v", err))
				return
			}
		}
	}
	err := s.Cfg.Update(func(cfg *config.Config) error {
		for i, e := range cfg.Registry {
			if e.Name == body.Original || e.Name == reg.Name {
				cfg.Registry[i] = reg
				return nil
			}
		}
		cfg.Registry = append(cfg.Registry, reg)
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) deleteRegistry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.Cfg.Update(func(cfg *config.Config) error {
		out := cfg.Registry[:0]
		for _, e := range cfg.Registry {
			if e.Name != name {
				out = append(out, e)
			}
		}
		cfg.Registry = out
		return nil
	})
	writeJSON(w, map[string]bool{"ok": true})
}

type settings struct {
	Allow       []string                `json:"allow"`
	Templates   []config.TemplateSource `json:"template_sources"`
	Auth        *config.Auth            `json:"auth,omitempty"`
	DataDir     string                  `json:"data_dir"`
	AuthInfo    map[string]any          `json:"auth_info,omitempty"`
	AgentListen string                  `json:"agent_listen,omitempty"`
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	snap := s.Cfg.Snapshot()
	allow := snap.Allow
	if allow == nil {
		allow = []string{}
	}
	writeJSON(w, settings{
		Allow: allow, Templates: snap.Templates, Auth: snap.Auth, DataDir: config.Dir(),
		AuthInfo:    map[string]any{"pam_built": auth.Available, "mode": i18n.T(reqLang(r), auth.Mode()), "uid": os.Geteuid()},
		AgentListen: s.AgentListen,
	})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body settings
	if err := readJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	// Validate before persisting so a typo can't lock everyone out.
	if err := s.Access.Set(append(s.extraAllow(), body.Allow...)); err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	if body.Auth != nil {
		a := body.Auth
		if !a.PAM && !a.TokenLogin {
			writeErr(w, badRequest("keep at least one sign-in method enabled (system users or token)"))
			return
		}
		if sess := sessionFrom(r.Context()); sess != nil {
			if sess.Method == "token" && !a.TokenLogin {
				writeErr(w, badRequest("you are signed in with the token: sign in with a system user before disabling it"))
				return
			}
			if sess.Method == "pam" {
				if role, err := (auth.Policy{AdminGroups: a.AdminGroups, ViewerGroups: a.ViewerGroups, AdminUsers: a.AdminUsers, ViewerUsers: a.ViewerUsers, AllowRoot: a.AllowRoot}).Role(sess.User); err != nil || role != auth.RoleAdmin {
					writeErr(w, badRequest("with these changes you would no longer be an admin; check groups and users"))
					return
				}
			}
		}
	}
	err := s.Cfg.Update(func(cfg *config.Config) error {
		cfg.Allow = body.Allow
		if body.Templates != nil {
			cfg.Templates = body.Templates
		}
		if body.Auth != nil {
			cfg.Auth = body.Auth
		}
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.roles.Clear()
	writeJSON(w, map[string]bool{"ok": true})
}

// ExtraAllow holds entries given with --allow on the command line, which
// are always active in addition to the persisted list.
var ExtraAllow []string

func (s *Server) extraAllow() []string { return append([]string{}, ExtraAllow...) }

// ReloadAccess re-applies the allowlist after the config changed on disk.
func (s *Server) ReloadAccess() {
	if err := s.Access.Set(append(s.extraAllow(), s.Cfg.Snapshot().Allow...)); err != nil {
		log.Printf("allowlist: %v", err)
	}
	s.roles.Clear()
}

func (s *Server) rotateToken(w http.ResponseWriter, r *http.Request) {
	token := config.NewToken()
	if err := s.Cfg.Update(func(cfg *config.Config) error { cfg.SetToken(token); return nil }); err != nil {
		writeErr(w, err)
		return
	}
	if sess := sessionFrom(r.Context()); sess != nil && sess.Method == "token" {
		ns := *sess
		ns.TokenHash = sessionTokenHash(config.HashToken(token))
		s.setCookie(w, ns)
	}
	writeJSON(w, map[string]string{"token": token})
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	list, status := s.Catalog.List(r.Context(), r.URL.Query().Get("refresh") == "1")
	writeJSON(w, map[string]any{"templates": list, "sources": status})
}

func (s *Server) templateCompose(w http.ResponseWriter, r *http.Request) {
	t, err := s.Catalog.Get(r.Context(), r.URL.Query().Get("id"))
	if err != nil {
		writeErr(w, &httpError{http.StatusNotFound, err})
		return
	}
	content, err := s.Catalog.ComposeFile(r.Context(), t)
	if err != nil {
		writeErr(w, &httpError{http.StatusBadGateway, err})
		return
	}
	writeJSON(w, map[string]string{"compose": content})
}

type roleInfo struct {
	Role   string           `json:"role"`
	Groups []auth.GroupInfo `json:"groups"`
	Users  []string         `json:"users"`
}

// accessInfo explains who can sign in (roles, groups and their members) and
// from where (listen address, allowlist and the caller's own address).
func (s *Server) accessInfo(w http.ResponseWriter, r *http.Request) {
	snap := s.Cfg.Snapshot()
	a := snap.Auth
	ip := access.RemoteIP(r, false)
	groups := func(names []string) []auth.GroupInfo {
		out := []auth.GroupInfo{}
		for _, n := range names {
			out = append(out, auth.Group(n))
		}
		return out
	}
	users := func(u []string) []string {
		if u == nil {
			return []string{}
		}
		return u
	}
	host, _, _ := net.SplitHostPort(s.Listen)
	hip, _ := netip.ParseAddr(host)
	me := "?"
	if u, err := user.Current(); err == nil {
		me = u.Username
	}
	allow := snap.Allow
	if allow == nil {
		allow = []string{}
	}
	writeJSON(w, map[string]any{
		"client_ip":     ip.String(),
		"client_rule":   s.Access.Match(ip),
		"listen":        s.Listen,
		"expose":        s.Expose,
		"tls":           s.Secure,
		"loopback_only": host == "localhost" || (hip.IsValid() && hip.IsLoopback()),
		"allow":         allow,
		"allow_flags":   s.extraAllow(),
		"service_user":  me,
		"os":            runtime.GOOS,
		"pam":           map[string]any{"enabled": a.PAM, "built": auth.Available, "mode": i18n.T(reqLang(r), auth.Mode())},
		"token_login":   a.TokenLogin,
		"allow_root":    a.AllowRoot,
		"roles": []roleInfo{
			{Role: auth.RoleAdmin, Groups: groups(a.AdminGroups), Users: users(a.AdminUsers)},
			{Role: auth.RoleViewer, Groups: groups(a.ViewerGroups), Users: users(a.ViewerUsers)},
		},
	})
}

// accessTest tells whether an address (or hostname) would be allowed.
func (s *Server) accessTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string `json:"address"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	addrs := []netip.Addr{}
	if a, err := netip.ParseAddr(strings.TrimSpace(body.Address)); err == nil {
		addrs = append(addrs, a)
	} else if ips, err := net.DefaultResolver.LookupNetIP(r.Context(), "ip", strings.TrimSpace(body.Address)); err == nil {
		addrs = ips
	} else {
		writeErr(w, badRequest("not an IP address or resolvable hostname: %s", body.Address))
		return
	}
	type result struct {
		IP   string `json:"ip"`
		Rule string `json:"rule"`
	}
	out := []result{}
	for _, a := range addrs {
		out = append(out, result{a.String(), s.Access.Match(a)})
	}
	writeJSON(w, out)
}
