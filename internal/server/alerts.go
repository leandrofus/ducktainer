package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ducktainer/internal/agent"
	"ducktainer/internal/config"
	"ducktainer/internal/docker"
)

const (
	alertTick      = 15 * time.Second
	alertReconcile = 30 * time.Second
	maxLogLine     = 8 << 10
)

// Alert is one firing/resolved state (host down, agent offline) or one
// event (log match). Only the former stay in the active list.
type Alert struct {
	Key       string     `json:"key"`
	RuleID    string     `json:"rule_id"`
	Rule      string     `json:"rule"`
	Kind      string     `json:"kind"`
	Endpoint  string     `json:"endpoint"`
	Container string     `json:"container,omitempty"`
	Message   string     `json:"message"`
	Since     time.Time  `json:"since"`
	Resolved  *time.Time `json:"resolved,omitempty"`
}

// alertEntry is a line of alerts.log: the history shown in the UI.
type alertEntry struct {
	Time      time.Time `json:"time"`
	State     string    `json:"state"` // firing | resolved | event
	RuleID    string    `json:"rule_id"`
	Rule      string    `json:"rule"`
	Kind      string    `json:"kind"`
	Endpoint  string    `json:"endpoint"`
	Container string    `json:"container,omitempty"`
	Message   string    `json:"message"`
}

type compiledRule struct {
	config.AlertRule
	re, skip []*regexp.Regexp
	// audit rules
	users, skipUsers, paths []*regexp.Regexp
}

func (r compiledRule) matches(line string) bool {
	for _, x := range r.skip {
		if x.MatchString(line) {
			return false
		}
	}
	for _, x := range r.re {
		if x.MatchString(line) {
			return true
		}
	}
	return false
}

func compileAll(list []string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for _, p := range list {
		if strings.TrimSpace(p) == "" {
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, nil
}

func (r compiledRule) covers(endpoint, container string) bool {
	if len(r.Endpoints) > 0 && !slices.Contains(r.Endpoints, endpoint) {
		return false
	}
	if container == "" || len(r.Containers) == 0 {
		return true
	}
	for _, g := range r.Containers {
		if ok, _ := path.Match(g, container); ok {
			return true
		}
	}
	return false
}

type alerter struct {
	s *Server

	mu       sync.Mutex
	rules    []compiledRule
	active   map[string]*Alert
	downAt   map[string]time.Time       // rule|endpoint -> first failed check
	lastLog  map[string]time.Time       // rule|endpoint|container -> last alert
	known    map[string]map[string]bool // user -> addresses it has signed in from
	failures map[string][]time.Time     // rule|ip -> recent failed sign-ins
	watchers map[string]context.CancelFunc

	logMu   sync.Mutex
	logPath string
}

func newAlerter(s *Server) *alerter {
	return &alerter{
		s:        s,
		active:   map[string]*Alert{},
		downAt:   map[string]time.Time{},
		lastLog:  map[string]time.Time{},
		known:    map[string]map[string]bool{},
		failures: map[string][]time.Time{},
		watchers: map[string]context.CancelFunc{},
		logPath:  pathJoinData("alerts.log"),
	}
}

func pathJoinData(name string) string { return config.Dir() + string(os.PathSeparator) + name }

// StartAlerts runs the watchers behind the alert rules.
func (s *Server) StartAlerts() {
	a := newAlerter(s)
	s.alerts = a
	a.seedKnownIPs()
	a.loadRules()
	s.audit.hook = a.onAudit
	go func() {
		// let the agents reconnect after a restart before judging them
		time.Sleep(alertTick)
		lastReconcile := time.Time{}
		for {
			a.loadRules()
			a.checkHosts()
			if time.Since(lastReconcile) >= alertReconcile {
				a.reconcileWatchers()
				lastReconcile = time.Now()
			}
			time.Sleep(alertTick)
		}
	}()
}

func (a *alerter) loadRules() {
	var out []compiledRule
	if al := a.s.Cfg.Snapshot().Alerts; al != nil {
		for _, r := range al.Rules {
			if !r.Enabled {
				continue
			}
			c := compiledRule{AlertRule: r}
			if r.Kind == "audit" {
				c.users, c.skipUsers, c.paths = globAll(r.Users), globAll(r.ExcludeUsers), globAll(r.Paths)
			}
			if r.Kind == "log_match" {
				var err error
				if c.re, err = compileAll(r.Patterns); err != nil || len(c.re) == 0 {
					continue
				}
				if c.skip, err = compileAll(r.Exclude); err != nil {
					continue
				}
			}
			out = append(out, c)
		}
	}
	a.mu.Lock()
	a.rules = out
	a.mu.Unlock()
}

func (a *alerter) ruleList() []compiledRule {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rules
}

// ---- hosts and agents ----

// stateKinds are the rules whose alerts stay active until resolved.
func stateKind(k string) bool {
	return k == "host_down" || k == "agent_offline" || k == "swarm_node" || k == "swarm_service"
}

func (a *alerter) checkHosts() {
	rules := a.ruleList()
	eps := a.s.Cfg.Snapshot().Endpoints
	live := map[string]bool{}
	for _, r := range rules {
		if r.Kind != "host_down" && r.Kind != "agent_offline" {
			continue
		}
		for _, ep := range eps {
			if ep.Agent() != (r.Kind == "agent_offline") || !r.covers(ep.Name, "") {
				continue
			}
			key := r.ID + "|" + ep.Name
			live[key] = true
			ok, why := a.endpointUp(ep)
			problem := ep.Name + " is unreachable"
			if r.Kind == "agent_offline" {
				problem = ep.Name + " agent is offline"
			}
			if why != "" {
				problem += ": " + why
			}
			a.track(r, ep.Name, key, ep.Name, ok, problem)
		}
	}
	a.checkSwarm(rules, eps, live)
	// state of rules (or hosts) that no longer exist
	a.mu.Lock()
	for key, al := range a.active {
		if stateKind(al.Kind) && !live[key] {
			delete(a.active, key) // removing a rule or host does not announce a recovery
		}
	}
	for key := range a.downAt {
		if !live[key] {
			delete(a.downAt, key)
		}
	}
	a.mu.Unlock()
}

func (a *alerter) endpointUp(ep config.Endpoint) (bool, string) {
	if ep.Agent() {
		if agent.DefaultHub.IsOnline(ep.Name) {
			return true, ""
		}
		return false, "the agent is not connected"
	}
	c, err := a.s.Client(ep.Name)
	if err != nil {
		return false, err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// track moves one watched thing between up and down: it fires once it has
// been down for the rule's grace period and resolves when it is back.
func (a *alerter) track(r compiledRule, endpoint, key, subject string, up bool, problem string) {
	a.mu.Lock()
	al := a.active[key]
	if up {
		delete(a.downAt, key)
		if al != nil {
			delete(a.active, key)
		}
		a.mu.Unlock()
		if al != nil {
			now := time.Now()
			al.Resolved = &now
			al.Message = fmt.Sprintf("%s recovered (after %s)", subject, now.Sub(al.Since).Round(time.Second))
			a.emit("resolved", r.AlertRule, al)
		}
		return
	}
	first, seen := a.downAt[key]
	if !seen {
		first = time.Now()
		a.downAt[key] = first
	}
	wait := time.Duration(r.ForSeconds) * time.Second
	if al != nil || time.Since(first) < wait {
		if al != nil && al.Message != problem {
			al.Message = problem // the reason changed: keep the list fresh without re-sending
		}
		a.mu.Unlock()
		return
	}
	al = &Alert{Key: key, RuleID: r.ID, Rule: r.Name, Kind: r.Kind, Endpoint: endpoint, Message: problem, Since: first}
	a.active[key] = al
	a.mu.Unlock()
	a.emit("firing", r.AlertRule, al)
}

// ---- logs ----

type alertContainer struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
}

func (c alertContainer) name() string {
	if len(c.Names) == 0 {
		return c.ID[:12]
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

func (a *alerter) reconcileWatchers() {
	rules := a.ruleList()
	want := map[string]struct{ ep, id, name string }{}
	for _, ep := range a.s.Cfg.Snapshot().Endpoints {
		var logRules []compiledRule
		for _, r := range rules {
			if r.Kind == "log_match" && r.covers(ep.Name, "") {
				logRules = append(logRules, r)
			}
		}
		if len(logRules) == 0 {
			continue
		}
		c, err := a.s.Client(ep.Name)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var list []alertContainer
		err = c.Get(ctx, "/containers/json", nil, &list)
		cancel()
		if err != nil {
			// unreachable: keep the watchers it has, they retry by themselves
			a.mu.Lock()
			for k := range a.watchers {
				if strings.HasPrefix(k, ep.Name+"\x00") {
					want[k] = struct{ ep, id, name string }{}
				}
			}
			a.mu.Unlock()
			continue
		}
		for _, ct := range list {
			for _, r := range logRules {
				if r.covers(ep.Name, ct.name()) {
					want[ep.Name+"\x00"+ct.ID] = struct{ ep, id, name string }{ep.Name, ct.ID, ct.name()}
					break
				}
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, cancel := range a.watchers {
		if _, ok := want[k]; !ok {
			cancel()
			delete(a.watchers, k)
		}
	}
	for k, w := range want {
		if _, ok := a.watchers[k]; ok || w.id == "" {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		a.watchers[k] = cancel
		go a.watch(ctx, w.ep, w.id, w.name)
	}
}

// watch follows one container's logs and matches every line against the
// rules that cover it.
func (a *alerter) watch(ctx context.Context, ep, id, name string) {
	since := time.Now()
	for ctx.Err() == nil {
		c, err := a.s.Client(ep)
		if err == nil {
			err = a.follow(ctx, c, ep, id, name, &since)
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("alerts: logs of %s on %s: %v", name, ep, err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	}
}

func (a *alerter) follow(ctx context.Context, c *docker.Client, ep, id, name string, since *time.Time) error {
	var in struct {
		Config map[string]json.RawMessage
	}
	if err := c.Get(ctx, "/containers/"+url.PathEscape(id)+"/json", nil, &in); err != nil {
		return err
	}
	tty := get[bool](in.Config, "Tty")
	q := url.Values{
		"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "follow": {"1"}, "tail": {"0"},
		"since": {strconv.FormatInt(since.Unix(), 10)},
	}
	resp, err := c.Do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", q, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	handle := func(line string) {
		ts, msg, ok := strings.Cut(line, " ")
		if !ok || !strings.Contains(ts, "T") {
			msg = line
		} else if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			*since = t.Add(time.Nanosecond)
		}
		a.matchLine(ep, name, msg)
	}
	split := func() func([]byte) {
		var pending []byte
		return func(p []byte) {
			pending = append(pending, p...)
			for {
				i := indexByte(pending, '\n')
				if i < 0 {
					if len(pending) > maxLogLine {
						handle(string(pending))
						pending = nil
					}
					return
				}
				handle(strings.TrimRight(string(pending[:i]), "\r"))
				pending = pending[i+1:]
			}
		}
	}
	if tty {
		out := split()
		br := bufio.NewReaderSize(resp.Body, 32<<10)
		buf := make([]byte, 32<<10)
		for {
			n, err := br.Read(buf)
			if n > 0 {
				out(buf[:n])
			}
			if err != nil {
				return nil
			}
		}
	}
	out, errs := split(), split()
	return docker.Demux(resp.Body, func(stream byte, p []byte) error {
		if stream == 2 {
			errs(p)
		} else {
			out(p)
		}
		return nil
	})
}

func (a *alerter) matchLine(ep, name, msg string) {
	for _, r := range a.ruleList() {
		if r.Kind != "log_match" || !r.covers(ep, name) || !r.matches(msg) {
			continue
		}
		key := r.ID + "|" + ep + "|" + name
		cool := time.Duration(r.CooldownMinutes) * time.Minute
		a.mu.Lock()
		last := a.lastLog[key]
		if time.Since(last) < cool {
			a.mu.Unlock()
			continue
		}
		a.lastLog[key] = time.Now()
		a.mu.Unlock()
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		a.emit("event", r.AlertRule, &Alert{
			Key: key, RuleID: r.ID, Rule: r.Name, Kind: r.Kind, Endpoint: ep, Container: name,
			Message: msg, Since: time.Now(),
		})
	}
}

// ---- output ----

// emit records the alert and sends it to the rule's channels.
func (a *alerter) emit(state string, r config.AlertRule, al *Alert) {
	e := alertEntry{Time: time.Now(), State: state, RuleID: al.RuleID, Rule: al.Rule, Kind: al.Kind, Endpoint: al.Endpoint, Container: al.Container, Message: al.Message}
	a.record(e)
	log.Printf("alert %s: %s (%s) %s", state, al.Rule, al.Endpoint, al.Message)
	var targets []config.AlertChannel
	if cfg := a.s.Cfg.Snapshot().Alerts; cfg != nil {
		for _, ch := range cfg.Channels {
			if len(r.Channels) == 0 || slices.Contains(r.Channels, ch.ID) {
				targets = append(targets, ch)
			}
		}
	}
	for _, ch := range targets {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := sendAlert(ctx, ch, e); err != nil {
				log.Printf("alerts: channel %q: %v", ch.Name, err)
			}
		}()
	}
}

func (a *alerter) record(e alertEntry) {
	data, _ := json.Marshal(e)
	a.logMu.Lock()
	defer a.logMu.Unlock()
	if st, err := os.Stat(a.logPath); err == nil && st.Size() > 4<<20 {
		os.Rename(a.logPath, a.logPath+".1")
	}
	f, err := os.OpenFile(a.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	config.FixOwner(a.logPath)
	f.Write(append(data, '\n'))
	f.Close()
}

// history returns the last n entries, newest first.
func (a *alerter) history(n int) []alertEntry {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	out := []alertEntry{}
	f, err := os.Open(a.logPath)
	if err != nil {
		return out
	}
	defer f.Close()
	var all []alertEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e alertEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			all = append(all, e)
			if len(all) > n*2 {
				all = all[len(all)-n:]
			}
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, all[i])
	}
	return out
}

func (a *alerter) activeList() []Alert {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Alert, 0, len(a.active))
	for _, al := range a.active {
		out = append(out, *al)
	}
	return out
}
