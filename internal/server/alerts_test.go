package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"ducktainer/internal/config"
)

func alertTestServer(t *testing.T, rules []config.AlertRule, hook string) *alerter {
	t.Helper()
	t.Setenv("DUCKTAINER_HOME", t.TempDir())
	t.Setenv("DUCKTAINER_KEY_FILE", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.Update(func(c *config.Config) error {
		c.Alerts = &config.Alerts{
			Channels: []config.AlertChannel{{ID: "w", Name: "hook", Kind: "webhook", URL: hook}},
			Rules:    rules,
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, nil, nil)
	a := newAlerter(s)
	s.alerts = a
	a.loadRules()
	return a
}

type hookLog struct {
	mu   sync.Mutex
	got  []map[string]any
	wait chan struct{}
}

func newHook(t *testing.T) (*hookLog, string) {
	h := &hookLog{wait: make(chan struct{}, 16)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		h.mu.Lock()
		h.got = append(h.got, m)
		h.mu.Unlock()
		h.wait <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	return h, srv.URL
}

func (h *hookLog) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case <-h.wait:
	case <-time.After(3 * time.Second):
		t.Fatal("no webhook call")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.got[len(h.got)-1]
}

func (h *hookLog) none(t *testing.T) {
	t.Helper()
	select {
	case <-h.wait:
		t.Fatal("unexpected webhook call")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestLogRuleMatchesAndCoolsDown(t *testing.T) {
	h, url := newHook(t)
	a := alertTestServer(t, []config.AlertRule{{
		ID: "r1", Name: "errors", Kind: "log_match", Enabled: true,
		Patterns: []string{`(?i)\berror\b`, `OOM`}, Exclude: []string{`healthcheck`}, Containers: []string{"web-*"}, CooldownMinutes: 5,
	}}, url)

	a.matchLine("local", "db", "ERROR: not watched")      // container outside the globs
	a.matchLine("local", "web-1", "all fine")             // no match
	a.matchLine("local", "web-1", "error in healthcheck") // excluded
	h.none(t)

	a.matchLine("local", "web-1", "2026 ERROR connection refused")
	got := h.next(t)
	if got["event"] != "event" || got["container"] != "web-1" || got["host"] != "local" {
		t.Fatalf("payload: %v", got)
	}
	a.matchLine("local", "web-1", "ERROR again") // inside the cooldown
	h.none(t)
	a.matchLine("local", "web-2", "error here") // another container has its own cooldown
	h.next(t)
	a.matchLine("local", "web-3", "killed by OOM") // second pattern
	h.next(t)

	if n := len(a.history(10)); n != 3 {
		t.Fatalf("history: %d entries", n)
	}
	if len(a.activeList()) != 0 {
		t.Fatal("a log event must not stay active")
	}
}

func TestHostDownFiresAfterGraceAndResolves(t *testing.T) {
	h, url := newHook(t)
	a := alertTestServer(t, []config.AlertRule{{ID: "r1", Name: "down", Kind: "host_down", Enabled: true, ForSeconds: 60}}, url)
	r := a.ruleList()[0]

	a.track(r, "srv", "r1|srv", "srv", false, "srv is unreachable: refused")
	h.none(t) // still inside the grace period
	a.mu.Lock()
	a.downAt["r1|srv"] = time.Now().Add(-2 * time.Minute)
	a.mu.Unlock()
	a.track(r, "srv", "r1|srv", "srv", false, "srv is unreachable: refused")
	if got := h.next(t); got["event"] != "firing" {
		t.Fatalf("payload: %v", got)
	}
	a.track(r, "srv", "r1|srv", "srv", false, "srv is unreachable: refused") // already firing: no repeat
	h.none(t)
	if len(a.activeList()) != 1 {
		t.Fatal("expected one active alert")
	}
	a.track(r, "srv", "r1|srv", "srv", true, "")
	if got := h.next(t); got["event"] != "resolved" {
		t.Fatalf("payload: %v", got)
	}
	if len(a.activeList()) != 0 {
		t.Fatal("still active after recovery")
	}
}

func TestAlertSecretsAreSealedOnDisk(t *testing.T) {
	a := alertTestServer(t, nil, "https://hooks.example.com/secret-path")
	raw, err := osReadFile(a.s.Cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if containsStr(raw, "secret-path") {
		t.Fatal("webhook URL stored in clear")
	}
	if got := a.s.Cfg.Snapshot().Alerts.Channels[0].URL; got != "https://hooks.example.com/secret-path" {
		t.Fatalf("snapshot URL: %q", got)
	}
}

func osReadFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }

func TestSwarmServiceAndNodeAlerts(t *testing.T) {
	h, url := newHook(t)
	a := alertTestServer(t, []config.AlertRule{
		{ID: "sv", Name: "svc", Kind: "swarm_service", Enabled: true, Containers: []string{"web*"}},
		{ID: "nd", Name: "nodes", Kind: "swarm_node", Enabled: true},
	}, url)
	rules := a.ruleList()
	live := map[string]bool{}

	var web, db swarmServiceInfo
	web.ID, web.Spec.Name = "s1", "web"
	web.ServiceStatus.DesiredTasks, web.ServiceStatus.RunningTasks = 3, 2
	db.ID, db.Spec.Name = "s2", "db" // outside the "web*" filter
	db.ServiceStatus.DesiredTasks = 1
	var task rawTask
	task.ServiceID, task.Slot = "s1", 3
	task.Status.State, task.Status.Err = "pending", "no suitable node (insufficient resources on 2 nodes)"

	var svRule, ndRule compiledRule
	for _, r := range rules {
		if r.Kind == "swarm_service" {
			svRule = r
		} else {
			ndRule = r
		}
	}
	a.checkSwarmServices(svRule, "mgr", "c1", []swarmServiceInfo{web, db}, []rawTask{task}, nil, live)
	got := h.next(t)
	msg, _ := got["message"].(string)
	if got["event"] != "firing" || !strings.Contains(msg, "2 of 3") || !strings.Contains(msg, "no suitable node") {
		t.Fatalf("payload: %v", got)
	}
	h.none(t) // db is not covered

	web.ServiceStatus.RunningTasks = 3
	a.checkSwarmServices(svRule, "mgr", "c1", []swarmServiceInfo{web, db}, nil, nil, live)
	if got := h.next(t); got["event"] != "resolved" {
		t.Fatalf("payload: %v", got)
	}

	var n1 swarmNodeInfo
	n1.ID, n1.Description.Hostname, n1.Status.State = "n1", "node-a", "down"
	a.checkSwarmNodes(ndRule, "mgr", "c1", []swarmNodeInfo{n1}, live)
	if got := h.next(t); got["event"] != "firing" || !strings.Contains(got["message"].(string), "node-a is down") {
		t.Fatalf("payload: %v", got)
	}
	n1.Status.State = "ready"
	a.checkSwarmNodes(ndRule, "mgr", "c1", []swarmNodeInfo{n1}, live)
	h.next(t)
}
