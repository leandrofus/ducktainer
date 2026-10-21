// Package server is ducktainer's HTTP API and web UI.
package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ducktainer/internal/access"
	"ducktainer/internal/auth"
	"ducktainer/internal/compose"
	"ducktainer/internal/config"
	"ducktainer/internal/docker"
	"ducktainer/internal/i18n"
	"ducktainer/internal/templates"
)

type Server struct {
	Cfg     *config.Config
	Access  *access.List
	Catalog *templates.Catalog
	Stacks  *compose.Store
	// SwarmStacks keeps the compose files of stacks deployed to Swarm.
	SwarmStacks *compose.Store
	UI          fs.FS
	Secure      bool // served over TLS: mark cookies Secure
	Version     string
	// Listen / Expose / AgentListen are the addresses serve was started with
	Listen      string
	Expose      string
	AgentListen string
	// TLSCert is the server certificate (DER), pinned by agent installers.
	TLSCert []byte
	// TunnelPorts is the port range for ephemeral forwards, e.g. 41000-41099
	TunnelPorts string

	mu      sync.Mutex
	clients map[string]*docker.Client

	limiter *auth.Limiter
	roles   sync.Map // user -> cachedRole
	audit   *auditLog
	tunnels tunnels
	expo    exposures
	alerts  *alerter
}

func New(cfg *config.Config, acl *access.List, ui fs.FS) *Server {
	return &Server{
		Cfg:         cfg,
		Access:      acl,
		Catalog:     templates.NewCatalog(cfg),
		Stacks:      &compose.Store{Root: config.Dir()},
		SwarmStacks: &compose.Store{Root: filepath.Join(config.Dir(), "swarm")},
		UI:          ui,
		clients:     map[string]*docker.Client{},
		limiter:     auth.NewLimiter(5, 10*time.Minute),
		audit:       newAuditLog(filepath.Join(config.Dir(), "audit.log")),
	}
}

// Client returns a cached docker client for an endpoint name.
func (s *Server) Client(name string) (*docker.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[name]; ok {
		return c, nil
	}
	ep, ok := s.Cfg.Endpoint(name)
	if !ok {
		return nil, herr(http.StatusNotFound, "unknown endpoint: %s", name)
	}
	c, err := docker.New(ep)
	if err != nil {
		return nil, err
	}
	s.clients[name] = c
	return c, nil
}

func (s *Server) dropClient(name string) {
	s.mu.Lock()
	delete(s.clients, name)
	s.mu.Unlock()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	mux.Handle("/", s.static())
	return s.Access.Middleware(false, withLang(securityHeaders(mux)))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.UI)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := s.UI.Open(p); err == nil {
				f.Close()
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback
		data, err := fs.ReadFile(s.UI, "index.html")
		if err != nil {
			http.Error(w, "UI not built: run `make web`", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	})
}

// auth lives in auth.go

// ---- helpers ----

type httpError struct {
	Status int
	Err    error
}

func (e *httpError) Error() string         { return e.Err.Error() }
func (e *httpError) In(lang string) string { return i18n.Render(e.Err, lang) }
func (e *httpError) Unwrap() error         { return e.Err }

// herr builds an HTTP error with a translatable message (English format).
func herr(status int, msg string, args ...any) error {
	return &httpError{status, i18n.Errorf(msg, args...)}
}

func badRequest(msg string, args ...any) error { return herr(http.StatusBadRequest, msg, args...) }

// ---- language ----

type langKey struct{}

// reqLang is the language of the current request (X-Lang, ?lang=,
// Accept-Language), set by withLang.
func reqLang(r *http.Request) string {
	if l, ok := r.Context().Value(langKey{}).(string); ok {
		return l
	}
	return "en"
}

type langWriter struct {
	http.ResponseWriter
	lang string
}

func (w *langWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *langWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	return h.Hijack()
}

func (w *langWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func langOf(w http.ResponseWriter) string {
	for {
		if l, ok := w.(*langWriter); ok {
			return l.lang
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return "en"
		}
		w = u.Unwrap()
	}
}

func withLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		explicit := r.Header.Get("X-Lang")
		if explicit == "" {
			explicit = r.URL.Query().Get("lang")
		}
		lang := i18n.Pick(explicit, r.Header.Get("Accept-Language"))
		r = r.WithContext(context.WithValue(r.Context(), langKey{}, lang))
		next.ServeHTTP(&langWriter{w, lang}, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var he *httpError
	var ae *docker.APIError
	switch {
	case errors.As(err, &he):
		status = he.Status
	case errors.As(err, &ae):
		status = ae.Status
	case errors.Is(err, context.Canceled):
		return
	}
	if status >= 500 {
		log.Printf("error: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": i18n.Render(err, langOf(w))})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	if err := dec.Decode(v); err != nil {
		return badRequest("invalid JSON: %v", err)
	}
	return nil
}

// stream writes newline-delimited JSON, flushing periodically. Safe for
// concurrent use.
type stream struct {
	lang   string
	mu     sync.Mutex
	w      *bufio.Writer
	f      http.Flusher
	closed bool
	done   chan struct{}
}

func newStream(w http.ResponseWriter) *stream {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f, _ := w.(http.Flusher)
	s := &stream{lang: langOf(w), w: bufio.NewWriterSize(w, 32<<10), f: f, done: make(chan struct{})}
	if f != nil {
		f.Flush()
	}
	go func() {
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.flush()
			case <-s.done:
				return
			}
		}
	}()
	return s
}

func (s *stream) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.w.Flush()
	if s.f != nil {
		s.f.Flush()
	}
}

func (s *stream) Send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	s.w.Write(data)
	_, err = s.w.Write([]byte{'\n'})
	return err
}

// Raw writes an already-encoded JSON line.
func (s *stream) Raw(line []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	s.w.Write(line)
	_, err := s.w.Write([]byte{'\n'})
	return err
}

// Log sends a translated progress line.
func (s *stream) Log(msg string, args ...any) {
	s.Send(map[string]string{"log": i18n.T(s.lang, msg, args...)})
}

// LogID sends a translated progress line carrying an object id.
func (s *stream) LogID(id, msg string, args ...any) {
	s.Send(map[string]string{"log": i18n.T(s.lang, msg, args...), "id": id})
}

func (s *stream) Close(err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		s.Send(map[string]string{"error": i18n.Render(err, s.lang)})
	} else if err == nil {
		s.Send(map[string]bool{"done": true})
	}
	s.flush()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	close(s.done)
}

// lineWriter turns text output (compose CLI) into {"log": line} events.
type lineWriter struct {
	s    *stream
	buf  []byte
	last string
}

func (lw *lineWriter) Write(p []byte) (int, error) {
	lw.buf = append(lw.buf, p...)
	for {
		i := strings.IndexAny(string(lw.buf), "\n\r")
		if i < 0 {
			break
		}
		if line := strings.TrimRight(string(lw.buf[:i]), " "); line != "" && line != lw.last {
			lw.s.Send(map[string]string{"log": line})
			lw.last = line
		}
		lw.buf = lw.buf[i+1:]
	}
	return len(p), nil
}

func (lw *lineWriter) Flush() {
	if len(lw.buf) > 0 {
		lw.s.Send(map[string]string{"log": string(lw.buf)})
		lw.last = strings.TrimSpace(string(lw.buf))
		lw.buf = nil
	}
}
