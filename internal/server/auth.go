package server

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"ducktainer/internal/i18n"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"ducktainer/internal/access"
	"ducktainer/internal/auth"
	"ducktainer/internal/config"
)

const cookieName = "ducktainer_session"

// Session is the signed content of the login cookie.
type Session struct {
	User   string `json:"u"`
	Role   string `json:"r"`
	Method string `json:"m"` // pam | token
	Exp    int64  `json:"e"`
	// TokenHash ties token sessions to the current token: rotating it
	// logs those sessions out.
	TokenHash string `json:"t,omitempty"`
}

type ctxKey struct{}

func sessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(ctxKey{}).(*Session)
	return s
}

// sessionTokenHash ties token sessions to the current token (a prefix of
// its stored hash): rotating the token revokes them.
func sessionTokenHash(stored string) string {
	if len(stored) < 12 {
		return stored
	}
	return stored[:12]
}

func (s *Server) sign(sess Session) string {
	payload, _ := json.Marshal(sess)
	p := base64.RawURLEncoding.EncodeToString(payload)
	m := hmac.New(sha256.New, []byte(s.Cfg.Snapshot().SessionSecret))
	m.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) verify(v string) (*Session, bool) {
	p, sig, ok := strings.Cut(v, ".")
	if !ok {
		return nil, false
	}
	m := hmac.New(sha256.New, []byte(s.Cfg.Snapshot().SessionSecret))
	m.Write([]byte(p))
	want := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		return nil, false
	}
	data, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, false
	}
	var sess Session
	if json.Unmarshal(data, &sess) != nil || time.Now().Unix() > sess.Exp {
		return nil, false
	}
	return &sess, true
}

func (s *Server) policy() auth.Policy {
	a := s.Cfg.Snapshot().Auth
	return auth.Policy{AdminGroups: a.AdminGroups, ViewerGroups: a.ViewerGroups, AdminUsers: a.AdminUsers, ViewerUsers: a.ViewerUsers, AllowRoot: a.AllowRoot}
}

type cachedRole struct {
	role string
	err  error
	at   time.Time
}

// currentRole re-resolves a system user's role every few minutes, so
// removing someone from a group revokes access without waiting for the
// cookie to expire.
func (s *Server) currentRole(user string) (string, error) {
	if v, ok := s.roles.Load(user); ok {
		c := v.(cachedRole)
		if time.Since(c.at) < 5*time.Minute {
			return c.role, c.err
		}
	}
	role, err := s.policy().Role(user)
	s.roles.Store(user, cachedRole{role, err, time.Now()})
	return role, err
}

// session authenticates the request: API bearer token or login cookie.
func (s *Server) session(r *http.Request) (*Session, error) {
	snap := s.Cfg.Snapshot()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if !snap.Auth.TokenLogin {
			return nil, i18n.Errorf("token access is disabled")
		}
		if !snap.CheckToken(strings.TrimPrefix(h, "Bearer ")) {
			return nil, i18n.Errorf("invalid token")
		}
		return &Session{User: "token", Role: auth.RoleAdmin, Method: "token"}, nil
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, i18n.Errorf("not authenticated")
	}
	sess, ok := s.verify(c.Value)
	if !ok {
		return nil, i18n.Errorf("session expired")
	}
	// Cookie-authenticated mutations must carry a header cross-site forms
	// can't set (CSRF). WebSockets are protected by the Origin check.
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Ducktainer") != "1" {
		return nil, i18n.Errorf("missing X-Ducktainer header")
	}
	switch sess.Method {
	case "token":
		if !snap.Auth.TokenLogin || sess.TokenHash != sessionTokenHash(snap.TokenHash) {
			return nil, i18n.Errorf("session revoked")
		}
	case "pam":
		if !snap.Auth.PAM {
			return nil, i18n.Errorf("system-user access is disabled")
		}
		role, err := s.currentRole(sess.User)
		if err != nil {
			return nil, i18n.Errorf("access revoked: %v", err)
		}
		sess.Role = role
	default:
		return nil, i18n.Errorf("invalid session")
	}
	return sess, nil
}

// Viewers can look but not touch: no mutations, no shells, no file
// downloads and no settings (which hold credentials).
var viewerDenied = regexp.MustCompile(`/(exec|files|download)$|/swarm/tokens$|^/api/(settings|registries|audit)`)

func viewerAllowed(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return !viewerDenied.MatchString(r.URL.Path)
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.session(r)
		if err != nil {
			writeErr(w, &httpError{http.StatusUnauthorized, err})
			return
		}
		if sess.Role != auth.RoleAdmin && !viewerAllowed(r) {
			writeErr(w, herr(http.StatusForbidden, "your role (%s) is read-only", sess.Role))
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess))
		if r.Method == http.MethodGet && !strings.HasSuffix(r.URL.Path, "/exec") {
			h(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		h(sw, r)
		s.audit.write(auditEntry{
			Time: start.UTC().Format(time.RFC3339), User: sess.User, Role: sess.Role, Method: r.Method,
			Path: r.URL.Path, Status: sw.status, IP: access.RemoteIP(r, false).String(),
		})
	}
}

func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request) {
		if sessionFrom(r.Context()).Role != auth.RoleAdmin {
			writeErr(w, herr(http.StatusForbidden, "requires the admin role"))
			return
		}
		h(w, r)
	})
}

func (s *Server) setCookie(w http.ResponseWriter, sess Session) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: s.sign(sess), Path: "/",
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteStrictMode,
		Expires: time.Unix(sess.Exp, 0),
	})
}

func (s *Server) authInfo(w http.ResponseWriter, r *http.Request) {
	a := s.Cfg.Snapshot().Auth
	writeJSON(w, map[string]any{"pam": a.PAM, "token": a.TokenLogin, "version": s.Version, "os": runtime.GOOS})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	snap := s.Cfg.Snapshot()
	ip := access.RemoteIP(r, false).String()
	body.Username = strings.TrimSpace(body.Username)
	keys := []string{"ip:" + ip}
	if body.Username != "" {
		keys = append(keys, "user:"+body.Username)
	}
	for _, k := range keys {
		if blocked, wait := s.limiter.Blocked(k); blocked {
			w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
			writeErr(w, herr(http.StatusTooManyRequests, "too many failed attempts; wait %d minutes", int(wait.Minutes())+1))
			return
		}
	}
	fail := func(reason error, status int) {
		msg := reason.Error()
		for _, k := range keys {
			s.limiter.Fail(k)
		}
		time.Sleep(600 * time.Millisecond)
		who := body.Username
		if who == "" {
			who = "token"
		}
		log.Printf("login failed: %s from %s: %s", who, ip, msg)
		s.audit.write(auditEntry{Time: time.Now().UTC().Format(time.RFC3339), User: who, Method: "LOGIN", Path: "/api/login", Status: status, IP: ip, Note: msg})
		writeErr(w, &httpError{status, reason})
	}
	exp := time.Now().Add(time.Duration(snap.Auth.SessionHours) * time.Hour).Unix()
	var sess Session
	switch {
	case body.Token != "":
		if !snap.Auth.TokenLogin {
			fail(i18n.Errorf("token access is disabled"), http.StatusForbidden)
			return
		}
		if !snap.CheckToken(strings.TrimSpace(body.Token)) {
			fail(i18n.Errorf("wrong token"), http.StatusUnauthorized)
			return
		}
		sess = Session{User: "token", Role: auth.RoleAdmin, Method: "token", Exp: exp, TokenHash: sessionTokenHash(snap.TokenHash)}
	case body.Username != "":
		if !snap.Auth.PAM {
			fail(i18n.Errorf("system-user access is disabled"), http.StatusForbidden)
			return
		}
		// never let the login form become a password oracle for root
		if body.Username == "root" && !snap.Auth.AllowRoot {
			fail(i18n.Errorf("root login is disabled"), http.StatusForbidden)
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if err := auth.Login(snap.Auth.PAMService, body.Username, body.Password, host); err != nil {
			status := http.StatusUnauthorized
			if errors.Is(err, auth.ErrUnavailable) {
				status = http.StatusServiceUnavailable
			}
			fail(err, status)
			return
		}
		s.roles.Delete(body.Username)
		role, err := s.currentRole(body.Username)
		if err != nil {
			fail(err, http.StatusForbidden)
			return
		}
		sess = Session{User: body.Username, Role: role, Method: "pam", Exp: exp}
	default:
		writeErr(w, badRequest("enter a username and password, or the token"))
		return
	}
	for _, k := range keys {
		s.limiter.Reset(k)
	}
	s.setCookie(w, sess)
	s.audit.write(auditEntry{Time: time.Now().UTC().Format(time.RFC3339), User: sess.User, Role: sess.Role, Method: "LOGIN", Path: "/api/login", Status: 200, IP: ip})
	writeJSON(w, map[string]any{"user": sess.User, "role": sess.Role})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteStrictMode})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	writeJSON(w, map[string]any{"user": sess.User, "role": sess.Role, "method": sess.Method, "version": s.Version})
}

// ---- audit ----

type auditEntry struct {
	Time   string `json:"time"`
	User   string `json:"user"`
	Role   string `json:"role,omitempty"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	IP     string `json:"ip"`
	Note   string `json:"note,omitempty"`
}

// auditLog appends who-did-what as JSON lines (and to the service log,
// i.e. journald under systemd).
type auditLog struct {
	mu   sync.Mutex
	path string
	hook func(auditEntry) // the alert engine, when running
}

func newAuditLog(path string) *auditLog { return &auditLog{path: path} }

func (a *auditLog) write(e auditEntry) {
	if a.hook != nil {
		go a.hook(e)
	}
	log.Printf("audit user=%s role=%s ip=%s %s %s -> %d %s", e.User, e.Role, e.IP, e.Method, e.Path, e.Status, e.Note)
	data, _ := json.Marshal(e)
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	config.FixOwner(a.path)
	f.Write(append(data, '\n'))
	f.Close()
}

// tail returns the last n entries, newest first.
func (a *auditLog) tail(n int) []auditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.Open(a.path)
	if err != nil {
		return []auditEntry{}
	}
	defer f.Close()
	var all []auditEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e auditEntry
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
	out := make([]auditEntry, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, all[i])
	}
	return out
}

func (s *Server) auditList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.audit.tail(500))
}

// statusWriter records the status for the audit log while keeping
// streaming (Flusher) and websockets (Hijacker) working.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	w.status = http.StatusSwitchingProtocols
	return h.Hijack()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
