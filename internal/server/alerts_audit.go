package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	destructivePath = regexp.MustCompile(`/(prune|kill)$`)
	configPath      = regexp.MustCompile(`^/api/(settings|endpoints|registries|alerts|templates|shortcuts|cloudflare|exposures|tunnels)(/|$)`)
)

// glob turns "web-*" style patterns into a regexp (* matches anything).
func globRegexp(g string) *regexp.Regexp {
	parts := strings.Split(g, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile("^(?i:" + strings.Join(parts, ".*") + ")$")
}

func globAll(list []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, g := range list {
		if strings.TrimSpace(g) != "" {
			out = append(out, globRegexp(strings.TrimSpace(g)))
		}
	}
	return out
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// auditMatches reports whether an audit entry is what an audit rule watches.
func (r compiledRule) auditMatches(e auditEntry) bool {
	if len(r.users) > 0 && !anyMatch(r.users, e.User) {
		return false
	}
	if anyMatch(r.skipUsers, e.User) {
		return false
	}
	done := e.Status > 0 && e.Status < 400 // the action went through
	login := e.Method == "LOGIN"
	switch r.Event {
	case "login_failed":
		return login && e.Status >= 400
	case "login_new_ip":
		return login && done
	case "destructive":
		return done && !login && (e.Method == http.MethodDelete || (e.Method == http.MethodPost && destructivePath.MatchString(e.Path)))
	case "terminal":
		return done && strings.HasSuffix(e.Path, "/exec")
	case "config_change":
		return done && !login && e.Method != http.MethodGet && configPath.MatchString(e.Path)
	case "custom":
		if len(r.Methods) > 0 && !containsFold(r.Methods, e.Method) {
			return false
		}
		return len(r.paths) == 0 || anyMatch(r.paths, e.Path)
	}
	return false
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// seedKnownIPs learns, from the audit log, where each user has signed in from.
func (a *alerter) seedKnownIPs() {
	f, err := os.Open(a.s.audit.path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e auditEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Method == "LOGIN" && e.Status == 200 {
			a.learnIP(e.User, e.IP)
		}
	}
}

func (a *alerter) learnIP(user, ip string) {
	if a.known[user] == nil {
		a.known[user] = map[string]bool{}
	}
	a.known[user][ip] = true
}

// onAudit feeds an audit entry to the audit rules.
func (a *alerter) onAudit(e auditEntry) {
	rules := a.ruleList()
	// the addresses a user signs in from are learned whatever the rules say
	a.mu.Lock()
	newIP := false
	if e.Method == "LOGIN" && e.Status == 200 {
		newIP = len(a.known[e.User]) > 0 && !a.known[e.User][e.IP]
		a.learnIP(e.User, e.IP)
	}
	a.mu.Unlock()

	for _, r := range rules {
		if r.Kind != "audit" || !r.auditMatches(e) {
			continue
		}
		var key, msg string
		switch r.Event {
		case "login_failed":
			if !a.failedLoginsReached(r, e) {
				continue
			}
			key = r.ID + "|ip:" + e.IP
			msg = fmt.Sprintf("%d failed sign-ins from %s in %d min (last as %q: %s)", r.Threshold, e.IP, r.WindowMinutes, e.User, e.Note)
		case "login_new_ip":
			if !newIP {
				continue
			}
			key = r.ID + "|" + e.User + "|" + e.IP
			msg = fmt.Sprintf("%s signed in from a new address, %s", e.User, e.IP)
		case "terminal":
			key = r.ID + "|" + e.User
			msg = fmt.Sprintf("%s (%s, %s) opened a terminal: %s", e.User, e.Role, e.IP, e.Path)
		default:
			key = r.ID + "|" + e.User
			msg = fmt.Sprintf("%s (%s, %s): %s %s → %d", e.User, e.Role, e.IP, e.Method, e.Path, e.Status)
		}
		cool := time.Duration(r.CooldownMinutes) * time.Minute
		if r.Event == "login_new_ip" {
			cool = 0 // each new address is reported once: it is known afterwards
		}
		a.mu.Lock()
		last := a.lastLog[key]
		if time.Since(last) < cool {
			a.mu.Unlock()
			continue
		}
		a.lastLog[key] = time.Now()
		a.mu.Unlock()
		a.emit("event", r.AlertRule, &Alert{Key: key, RuleID: r.ID, Rule: r.Name, Kind: "audit", Message: msg, Since: time.Now()})
	}
}

// failedLoginsReached counts a failed sign-in and says whether this address
// has reached the rule's threshold within its window.
func (a *alerter) failedLoginsReached(r compiledRule, e auditEntry) bool {
	window := time.Duration(r.WindowMinutes) * time.Minute
	key := r.ID + "|" + e.IP
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	keep := a.failures[key][:0]
	for _, t := range a.failures[key] {
		if now.Sub(t) < window {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	a.failures[key] = keep
	return len(keep) >= r.Threshold
}
