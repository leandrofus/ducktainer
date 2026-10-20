// Package config holds ducktainer's persistent settings: access token, allowed
// hosts, docker endpoints, registries and marketplace sources.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"ducktainer/internal/dockersock"
)

type Endpoint struct {
	Name string `json:"name"`
	// URL is unix:///var/run/docker.sock, ssh://user@host[:port] or
	// tcp://host:port (a remote `ducktainer expose`).
	URL string `json:"url"`
	// Token for tcp endpoints exposed by `ducktainer expose`.
	Token string `json:"token,omitempty"`
	TLS   bool   `json:"tls,omitempty"`
	// Fingerprint pins the remote certificate (sha256, hex). Empty with TLS
	// on means "accept any" (still encrypted, not authenticated).
	Fingerprint string `json:"fingerprint,omitempty"`
	// SSHKey is an optional identity file for ssh endpoints.
	SSHKey string `json:"ssh_key,omitempty"`
	// DockerSocket is the daemon socket path on the node itself, mounted
	// into the helper that runs compose there (default /var/run/docker.sock).
	DockerSocket string `json:"docker_socket,omitempty"`
	// ComposeImage provides `docker compose` on remote nodes (default docker:cli).
	ComposeImage string `json:"compose_image,omitempty"`
}

// Local reports whether the endpoint is the daemon of the machine running
// ducktainer, so stack files are read and compose runs right here.
func (e Endpoint) Local() bool {
	return strings.HasPrefix(e.URL, "unix://") || strings.HasPrefix(e.URL, "npipe://")
}

// Agent reports whether the node connects to ducktainer with the outbound agent.
func (e Endpoint) Agent() bool { return strings.HasPrefix(e.URL, "agent:") }

type Registry struct {
	Name     string `json:"name"`
	Server   string `json:"server"` // e.g. docker.io, ghcr.io, registry.example.com:5000
	Username string `json:"username"`
	Password string `json:"password"`
	// Endpoints limits the hosts that receive these credentials (compose
	// runs on the node get a copy); empty means every host.
	Endpoints []string `json:"endpoints,omitempty"`
}

// For reports whether the credentials may be handed to an endpoint.
func (r Registry) For(endpoint string) bool {
	if len(r.Endpoints) == 0 {
		return true
	}
	for _, e := range r.Endpoints {
		if e == endpoint {
			return true
		}
	}
	return false
}

// Cloudflare is the account used for tunnels on your own domains. The API
// token and the tunnel token are sealed on disk.
type Cloudflare struct {
	APIToken    string `json:"api_token,omitempty"`
	AccountID   string `json:"account_id,omitempty"`
	AccountName string `json:"account_name,omitempty"`
	TunnelID    string `json:"tunnel_id,omitempty"`
	TunnelName  string `json:"tunnel_name,omitempty"`
	TunnelToken string `json:"tunnel_token,omitempty"`
}

// ExposeAccess decides who reaches an exposed port. Empty means public.
// IPs must match (when set) and then a login or the secret link is needed
// (when either is enabled).
type ExposeAccess struct {
	IPs    []string `json:"ips,omitempty"` // IPs, CIDRs or hostnames
	Login  bool     `json:"login,omitempty"`
	Users  []string `json:"users,omitempty"`  // with Login: these users…
	Groups []string `json:"groups,omitempty"` // …or members of these groups (none: any ducktainer user)
	// LinkKey grants access to whoever opens the link carrying it (sealed).
	LinkKey string `json:"link_key,omitempty"`
}

// Exposure publishes a container port on the internet through Cloudflare:
// a quick tunnel (random trycloudflare.com address, no account) or a
// hostname of the account's named tunnel.
type Exposure struct {
	ID        string       `json:"id"`
	Kind      string       `json:"kind"` // quick | domain
	Endpoint  string       `json:"endpoint"`
	Container string       `json:"container"` // name, so it survives a recreate
	Port      int          `json:"port"`      // container port
	Scheme    string       `json:"scheme,omitempty"`
	Hostname  string       `json:"hostname,omitempty"` // domain
	ZoneID    string       `json:"zone_id,omitempty"`
	DNSRecord string       `json:"dns_record,omitempty"`
	Access    ExposeAccess `json:"access"`
	Expires   *time.Time   `json:"expires,omitempty"`
	User      string       `json:"user"`
	Created   time.Time    `json:"created"`
}

// Forward is a port forward kept across restarts: for some hours or until
// closed (temporary ones that close when idle live only in memory).
type Forward struct {
	ID        string     `json:"id"`
	Endpoint  string     `json:"endpoint"`
	Container string     `json:"container"` // name, so it survives a recreate
	Port      int        `json:"port"`      // container port
	Local     int        `json:"local_port"`
	Mode      string     `json:"mode"` // until | persistent
	Until     *time.Time `json:"until,omitempty"`
	// Shared lets every address allowed to reach ducktainer use it;
	// otherwise only Client.
	Shared  bool      `json:"shared,omitempty"`
	Client  string    `json:"client"`
	User    string    `json:"user"`
	Created time.Time `json:"created"`
}

// ShortcutRef is a container web port with an entry in the start menu.
type ShortcutRef struct {
	Endpoint  string `json:"endpoint"`
	Container string `json:"container"` // name, so it survives a recreate
	Port      int    `json:"port"`      // container port
}

// Shortcuts: start menu entries on the machine running ducktainer.
type Shortcuts struct {
	All   bool          `json:"all,omitempty"` // every published web port of this machine's containers
	Items []ShortcutRef `json:"items,omitempty"`
}

type TemplateSource struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// Auth controls who can log in. System users (PAM) map to roles through
// their POSIX groups; the token is a break-glass admin credential.
type Auth struct {
	PAM          bool     `json:"pam"`
	PAMService   string   `json:"pam_service"`
	TokenLogin   bool     `json:"token_login"`
	AdminGroups  []string `json:"admin_groups"`
	ViewerGroups []string `json:"viewer_groups"`
	AdminUsers   []string `json:"admin_users"`
	ViewerUsers  []string `json:"viewer_users"`
	AllowRoot    bool     `json:"allow_root"`
	SessionHours int      `json:"session_hours"`
}

// DefaultAuth: members of `docker` (Administrators and docker-users on
// Windows) can already control the daemon, so they are admins; extra groups
// are created by the packages.
func DefaultAuth(pamAvailable bool) *Auth {
	admins := DefaultAdminGroups
	if len(admins) == 0 {
		admins = []string{"docker", "ducktainer-admins"}
	}
	return &Auth{
		PAM:          pamAvailable,
		PAMService:   "ducktainer",
		TokenLogin:   true,
		AdminGroups:  append([]string(nil), admins...),
		ViewerGroups: []string{"ducktainer-viewers"},
		SessionHours: 12,
	}
}

// PAMAvailable and DefaultAdminGroups are set by main from the auth package
// (avoids an import cycle).
var (
	PAMAvailable       bool
	DefaultAdminGroups []string
)

type Config struct {
	// TokenHash is the SHA-256 of the access token; the token itself is
	// never stored (see SetToken, CheckToken).
	TokenHash string `json:"token_hash"`
	// SessionSecret signs login cookies; rotating it logs everyone out.
	// Like endpoint tokens and registry passwords it is sealed on disk.
	SessionSecret string           `json:"session_secret"`
	Auth          *Auth            `json:"auth"`
	Allow         []string         `json:"allow"`
	Endpoints     []Endpoint       `json:"endpoints"`
	Registry      []Registry       `json:"registries"`
	Templates     []TemplateSource `json:"template_sources"`
	Cloudflare    *Cloudflare      `json:"cloudflare,omitempty"`
	Exposures     []Exposure       `json:"exposures,omitempty"`
	Shortcuts     *Shortcuts       `json:"shortcuts,omitempty"`
	Forwards      []Forward        `json:"forwards,omitempty"`

	mu       sync.Mutex
	path     string
	mtime    time.Time
	key      []byte
	newToken string // plaintext token, only when generated by this process
	legacy   bool   // decoded plaintext secrets: rewrite sealed
}

var DefaultTemplateSources = []TemplateSource{
	{Name: "Portainer official", URL: "https://raw.githubusercontent.com/portainer/templates/v3/templates.json", Enabled: true},
	{Name: "Lissy93 (community, ~800 apps)", URL: "https://raw.githubusercontent.com/Lissy93/portainer-templates/main/templates.json", Enabled: false},
}

// Dir returns the data directory: $DUCKTAINER_HOME, /var/lib/ducktainer for root,
// or $XDG_CONFIG_HOME/ducktainer. On Windows the service (and an elevated
// administrator, once it's installed) uses %ProgramData%\ducktainer.
func Dir() string {
	if d := os.Getenv("DUCKTAINER_HOME"); d != "" {
		return d
	}
	if d := machineDir(); d != "" {
		return d
	}
	if os.Geteuid() == 0 {
		return "/var/lib/ducktainer"
	}
	if u, err := user.Current(); err == nil && u.Username == "ducktainer" {
		return "/var/lib/ducktainer" // the packaged service user
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "ducktainer")
	}
	return ".ducktainer"
}

func NewToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func Load() (*Config, error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := loadKey()
	if err != nil {
		return nil, err
	}
	c := &Config{path: filepath.Join(dir, "config.json"), key: key}
	data, err := os.ReadFile(c.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		c.SetToken(NewToken())
		c.SessionSecret = NewToken()
		c.Auth = DefaultAuth(PAMAvailable)
		c.Templates = append([]TemplateSource(nil), DefaultTemplateSources...)
		c.Endpoints = []Endpoint{{Name: "local", URL: defaultLocalURL()}}
		return c, c.Save()
	case err != nil:
		return nil, err
	}
	if err := c.decode(data); err != nil {
		return nil, err
	}
	if c.legacy { // migrate plaintext secrets right away
		if err := c.Save(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// decode parses the file into c and fills defaults (callers hold c.mu or
// own c exclusively).
func (c *Config) decode(data []byte) error {
	var o struct {
		Config
		LegacyToken string `json:"token"` // before it was hashed
	}
	if err := json.Unmarshal(data, &o); err != nil {
		return fmt.Errorf("%s: %w", c.path, err)
	}
	if c.key == nil {
		k, err := loadKey()
		if err != nil {
			return err
		}
		c.key = k
	}
	legacy := false
	open := func(v string) (string, error) {
		plain, old, err := unseal(c.key, v)
		legacy = legacy || old
		return plain, err
	}
	var err error
	if o.SessionSecret, err = open(o.SessionSecret); err != nil {
		return err
	}
	for i := range o.Endpoints {
		if o.Endpoints[i].Token, err = open(o.Endpoints[i].Token); err != nil {
			return err
		}
	}
	for i := range o.Registry {
		if o.Registry[i].Password, err = open(o.Registry[i].Password); err != nil {
			return err
		}
	}
	if cf := o.Cloudflare; cf != nil {
		if cf.APIToken, err = open(cf.APIToken); err != nil {
			return err
		}
		if cf.TunnelToken, err = open(cf.TunnelToken); err != nil {
			return err
		}
	}
	for i := range o.Exposures {
		if o.Exposures[i].Access.LinkKey, err = open(o.Exposures[i].Access.LinkKey); err != nil {
			return err
		}
	}
	c.TokenHash, c.SessionSecret, c.Auth = o.TokenHash, o.SessionSecret, o.Auth
	c.Cloudflare, c.Exposures, c.Shortcuts, c.Forwards = o.Cloudflare, o.Exposures, o.Shortcuts, o.Forwards
	c.Allow, c.Endpoints, c.Registry, c.Templates = o.Allow, o.Endpoints, o.Registry, o.Templates
	if o.LegacyToken != "" {
		c.SetToken(o.LegacyToken)
		legacy = true
	}
	if c.TokenHash == "" {
		c.SetToken(NewToken())
		legacy = true
	}
	if c.SessionSecret == "" {
		c.SessionSecret = NewToken()
	}
	c.legacy = legacy
	if c.Auth == nil { // configs from before system users existed
		c.Auth = DefaultAuth(PAMAvailable)
	}
	if c.Auth.PAMService == "" {
		c.Auth.PAMService = "ducktainer"
	}
	if c.Auth.SessionHours <= 0 {
		c.Auth.SessionHours = 12
	}
	if len(c.Endpoints) == 0 {
		c.Endpoints = []Endpoint{{Name: "local", URL: defaultLocalURL()}}
	}
	return nil
}

func (c *Config) Path() string { return c.path }

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.save()
}

func (c *Config) save() error {
	if c.key == nil {
		k, err := loadKey()
		if err != nil {
			return err
		}
		c.key = k
	}
	disk := c.clone()
	var err error
	if disk.SessionSecret, err = seal(c.key, disk.SessionSecret); err != nil {
		return err
	}
	for i := range disk.Endpoints {
		if disk.Endpoints[i].Token, err = seal(c.key, disk.Endpoints[i].Token); err != nil {
			return err
		}
	}
	for i := range disk.Registry {
		if disk.Registry[i].Password, err = seal(c.key, disk.Registry[i].Password); err != nil {
			return err
		}
	}
	if cf := disk.Cloudflare; cf != nil {
		if cf.APIToken, err = seal(c.key, cf.APIToken); err != nil {
			return err
		}
		if cf.TunnelToken, err = seal(c.key, cf.TunnelToken); err != nil {
			return err
		}
	}
	for i := range disk.Exposures {
		if disk.Exposures[i].Access.LinkKey, err = seal(c.key, disk.Exposures[i].Access.LinkKey); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	// a unique temporary file: the service and the CLI may save at once
	f, err := os.CreateTemp(filepath.Dir(c.path), "config.json.*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		FixOwner(tmp)
		err = replaceFile(tmp, c.path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if st, err := os.Stat(c.path); err == nil {
		c.mtime = st.ModTime()
	}
	c.legacy = false
	return nil
}

// Update re-reads the file (another process — the CLI, the service — may
// have changed it), applies fn and persists the result.
func (c *Config) Update(fn func(c *Config) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if data, err := os.ReadFile(c.path); err == nil {
		if err := c.decode(data); err != nil {
			return err
		}
	}
	if err := fn(c); err != nil {
		return err
	}
	return c.save()
}

// Watch reloads the config when the file is changed by someone else and
// calls onChange afterwards (e.g. `sudo ducktainer allow add` while serving).
func (c *Config) Watch(onChange func()) {
	go func() {
		for range time.Tick(3 * time.Second) {
			st, err := os.Stat(c.path)
			if err != nil {
				continue
			}
			c.mu.Lock()
			changed := !st.ModTime().Equal(c.mtime)
			if changed {
				if data, err := os.ReadFile(c.path); err == nil && c.decode(data) == nil {
					c.mtime = st.ModTime()
				} else {
					changed = false
				}
			}
			c.mu.Unlock()
			if changed {
				onChange()
			}
		}
	}()
}

// FixOwner gives files written by root (sudo ducktainer …) to the owner of
// the data directory, so a service running as an unprivileged user can
// still read them.
func FixOwner(path string) {
	if os.Geteuid() != 0 {
		return
	}
	if uid, gid, ok := owner(filepath.Dir(path)); ok && uid != 0 {
		_ = os.Lchown(path, uid, gid)
	}
}

// Snapshot returns a copy safe to read without holding the lock.
func (c *Config) Snapshot() *Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clone()
}

// clone copies the settings (callers hold c.mu).
func (c *Config) clone() *Config {
	var a *Auth
	if c.Auth != nil {
		x := *c.Auth
		a = &x
	}
	var cf *Cloudflare
	if c.Cloudflare != nil {
		x := *c.Cloudflare
		cf = &x
	}
	xs := make([]Exposure, len(c.Exposures))
	for i, x := range c.Exposures {
		x.Access.IPs = append([]string(nil), x.Access.IPs...)
		x.Access.Users = append([]string(nil), x.Access.Users...)
		x.Access.Groups = append([]string(nil), x.Access.Groups...)
		xs[i] = x
	}
	var sc *Shortcuts
	if c.Shortcuts != nil {
		sc = &Shortcuts{All: c.Shortcuts.All, Items: append([]ShortcutRef(nil), c.Shortcuts.Items...)}
	}
	return &Config{
		Forwards:      append([]Forward(nil), c.Forwards...),
		Shortcuts:     sc,
		Cloudflare:    cf,
		Exposures:     xs,
		TokenHash:     c.TokenHash,
		SessionSecret: c.SessionSecret,
		Auth:          a,
		Allow:         append([]string(nil), c.Allow...),
		Endpoints:     append([]Endpoint(nil), c.Endpoints...),
		Registry:      append([]Registry(nil), c.Registry...),
		Templates:     append([]TemplateSource(nil), c.Templates...),
	}
}

func (c *Config) Endpoint(name string) (Endpoint, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.Endpoints {
		if e.Name == name {
			return e, true
		}
	}
	return Endpoint{}, false
}

func defaultLocalURL() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	if runtime.GOOS == "windows" {
		return "npipe:////./pipe/docker_engine"
	}
	return "unix://" + dockersock.Path()
}
