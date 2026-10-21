package agent

import (
	"context"
	"ducktainer/internal/i18n"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Hub manages active agent sessions established via outbound reverse tunnels.
type Hub struct {
	mu       sync.RWMutex
	sessions map[string]*yamux.Session
	addrs    map[string]string // address each agent connected from
	versions map[string]string // version each agent reported
}

// DefaultHub is the singleton agent hub used by the ducktainer server and client.
var DefaultHub = NewHub()

func NewHub() *Hub {
	return &Hub{
		sessions: make(map[string]*yamux.Session),
		addrs:    make(map[string]string),
		versions: make(map[string]string),
	}
}

// Register stores an active agent session under the given name, closing any
// existing session with the same name.
func (h *Hub) Register(name string, s *yamux.Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if prev, ok := h.sessions[name]; ok && prev != s {
		_ = prev.Close()
	}
	h.sessions[name] = s
}

// SetAddr records the address an agent connected from: a good default for
// reaching its published ports, unless it sits behind NAT.
func (h *Hub) SetAddr(name, addr string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.addrs[name] = addr
}

// SetVersion records the version an agent reported.
func (h *Hub) SetVersion(name, version string) {
	if version == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.versions[name] = version
}

// Version returns the version recorded by SetVersion ("" if never reported).
func (h *Hub) Version(name string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.versions[name]
}

// Addr returns the address recorded by SetAddr.
func (h *Hub) Addr(name string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.addrs[name]
}

// Unregister removes the session if it matches the current one for name.
func (h *Hub) Unregister(name string, s *yamux.Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if curr, ok := h.sessions[name]; ok && curr == s {
		delete(h.sessions, name)
	}
}

// IsOnline checks whether an agent session is active and not closed.
func (h *Hub) IsOnline(name string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.sessions[name]
	return ok && !s.IsClosed()
}

// ListOnline returns all currently connected agent names.
func (h *Hub) ListOnline() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []string
	for name, s := range h.sessions {
		if !s.IsClosed() {
			out = append(out, name)
		}
	}
	return out
}

// Dial opens a multiplexed stream to the agent with the given name.
// The returned net.Conn is directly connected to the remote agent's Docker daemon socket.
func (h *Hub) Dial(ctx context.Context, name string) (net.Conn, error) {
	h.mu.RLock()
	s, ok := h.sessions[name]
	h.mu.RUnlock()

	if !ok || s.IsClosed() {
		return nil, i18n.Errorf("agent %s is offline (waiting for connection)", name)
	}

	type streamResult struct {
		stream *yamux.Stream
		err    error
	}

	resCh := make(chan streamResult, 1)
	go func() {
		stream, err := s.OpenStream()
		resCh <- streamResult{stream: stream, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-resCh:
		if res.err != nil {
			return nil, i18n.Errorf("agent %s stream failed: %w", name, res.err)
		}
		return res.stream, nil
	case <-time.After(15 * time.Second):
		return nil, i18n.Errorf("timeout opening stream to agent %s", name)
	}
}

// Dial is a convenience helper calling DefaultHub.Dial.
func Dial(ctx context.Context, name string) (net.Conn, error) {
	return DefaultHub.Dial(ctx, name)
}
