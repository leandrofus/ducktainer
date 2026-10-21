package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ducktainer/internal/access"
	"ducktainer/internal/agent"
	"ducktainer/internal/config"
	"ducktainer/internal/docker"
	"ducktainer/internal/server"
)

func newTestServer(t *testing.T) (*server.Server, *config.Config, func()) {
	tmpDir, err := os.MkdirTemp("", "ducktainer-srv-test-*")
	if err != nil {
		t.Fatal(err)
	}
	os.Setenv("DUCKTAINER_HOME", tmpDir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg.Update(func(c *config.Config) error { c.SetToken("test-admin-token"); return nil })

	acl, err := access.New([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	srv := server.New(cfg, acl, os.DirFS(tmpDir))
	cleanup := func() {
		os.RemoveAll(tmpDir)
		os.Unsetenv("DUCKTAINER_HOME")
	}
	return srv, cfg, cleanup
}

// addAgentHost registers an agent endpoint with its own token.
func addAgentHost(t *testing.T, cfg *config.Config, name, token string) {
	t.Helper()
	if err := cfg.Update(func(c *config.Config) error {
		c.Endpoints = append(c.Endpoints, config.Endpoint{Name: name, URL: "agent://" + name, Token: token})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func get(h http.Handler, path, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	req.Host = "ducktainer.central:9900"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func TestAgentInstallScriptAndBinary(t *testing.T) {
	srv, cfg, cleanup := newTestServer(t)
	defer cleanup()
	addAgentHost(t, cfg, "edge-node", "edge-token")
	handler := srv.Handler()

	if rec := get(handler, "/api/agent/binary", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("binary without token: got %d, want 401", rec.Code)
	}
	// the admin token is not an agent credential
	if rec := get(handler, "/api/agent/binary", "test-admin-token"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("binary with the admin token: got %d, want 401", rec.Code)
	}
	if rec := get(handler, "/api/agent/install.sh", "test-admin-token"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("install.sh with the admin token: got %d, want 401", rec.Code)
	}
	// tokens in the URL are not accepted any more
	if rec := get(handler, "/api/agent/binary?token=edge-token", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("binary with ?token=: got %d, want 401", rec.Code)
	}

	rec := get(handler, "/api/agent/binary", "edge-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("binary with the host token: got %d, want 200", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "ducktainer-agent") {
		t.Fatalf("expected Content-Disposition header, got %q", cd)
	}

	// the token decides the node; ?name= cannot pick another one
	rec = get(handler, "/api/agent/install.sh?name=other", "edge-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("install.sh: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"#!/bin/sh", "NAME='edge-node'", "TOKEN='edge-token'", "EnvironmentFile=/etc/ducktainer-agent/agent.env"} {
		if !strings.Contains(body, want) {
			t.Fatalf("install script lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "--insecure") || strings.Contains(body, "--token") {
		t.Fatalf("install script must not use --insecure or put the token on the command line:\n%s", body)
	}
	if runtime.GOOS == "windows" {
		return // no sh to check the syntax with
	}
	if out, err := exec.Command("sh", "-n", "-c", body).CombinedOutput(); err != nil {
		t.Fatalf("install script is not valid sh: %v\n%s", err, out)
	}
}

func TestAgentTunnelAuth(t *testing.T) {
	srv, cfg, cleanup := newTestServer(t)
	defer cleanup()
	addAgentHost(t, cfg, "worker-1", "w1-token")
	addAgentHost(t, cfg, "worker-2", "w2-token")

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	tmpDir, _ := os.MkdirTemp("", "ducktainer-agent-mock-*")
	defer os.RemoveAll(tmpDir)
	mockSock := filepath.Join(tmpDir, "docker.sock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := func(name, token string) {
		go func() { _ = agent.Run(ctx, agent.Config{Server: ts.URL, Token: token, Name: name, Socket: mockSock}) }()
	}
	online := func(name string) bool {
		for i := 0; i < 40; i++ {
			if agent.DefaultHub.IsOnline(name) {
				return true
			}
			time.Sleep(50 * time.Millisecond)
		}
		return false
	}

	// the admin token neither registers new hosts nor opens existing ones
	run("auto-worker", "test-admin-token")
	run("worker-2", "test-admin-token")
	// another host's token cannot impersonate a node
	run("worker-2", "w1-token")
	if online("auto-worker") || agent.DefaultHub.IsOnline("worker-2") {
		t.Fatal("an agent connected without its own token")
	}
	if _, ok := cfg.Endpoint("auto-worker"); ok {
		t.Fatal("the admin token auto-registered a host")
	}

	// hosts auto-registered by earlier versions kept the admin token as their own
	addAgentHost(t, cfg, "legacy", "test-admin-token")
	run("legacy", "test-admin-token")

	run("worker-1", "w1-token")
	if !online("worker-1") {
		t.Fatal("worker-1 did not connect with its own token")
	}
	if agent.DefaultHub.IsOnline("legacy") {
		t.Fatal("a host holding the admin token connected")
	}
}

func TestSecureTCPEndpoint(t *testing.T) {
	if _, err := docker.Secure(context.Background(), config.Endpoint{Name: "n", URL: "tcp://10.1.2.3:2376"}); err == nil {
		t.Fatal("a remote tcp endpoint without TLS was accepted")
	}
	if _, err := docker.Secure(context.Background(), config.Endpoint{Name: "n", URL: "tcp://127.0.0.1:2376"}); err != nil {
		t.Fatalf("loopback without TLS: %v", err)
	}

	ts := httptest.NewTLSServer(http.NotFoundHandler())
	defer ts.Close()
	sum := sha256.Sum256(ts.Certificate().Raw)
	ep, err := docker.Secure(context.Background(), config.Endpoint{Name: "n", URL: "tcp://" + ts.Listener.Addr().String(), TLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Fingerprint != hex.EncodeToString(sum[:]) {
		t.Fatalf("fingerprint = %q, want the server certificate's", ep.Fingerprint)
	}
}

func TestInstallScriptChecksBeforeTouchingTheNode(t *testing.T) {
	srv, cfg, cleanup := newTestServer(t)
	defer cleanup()
	addAgentHost(t, cfg, "edge-node", "edge-token")
	rec := get(srv.Handler(), "/api/agent/install.sh?server=https%3A%2F%2F10.0.0.1%3A9900", "edge-token")
	script := rec.Body.String()
	modify := strings.Index(script, "install -d -m 0700 /etc/ducktainer-agent")
	if rec.Code != http.StatusOK || modify < 0 {
		t.Fatalf("install.sh: %d\n%s", rec.Code, script)
	}
	// everything that can make the install fail runs before the node is modified
	for _, step := range []string{`uname -m`, `/api/agent/binary`, `"$BIN.new" version`, `GLIBC`} {
		if at := strings.Index(script, step); at < 0 || at > modify {
			t.Errorf("%q must run before the node is modified", step)
		}
	}
	if !strings.Contains(script, `[ "$ARCH" = "`+runtime.GOARCH+`" ]`) {
		t.Error("the architecture check is missing")
	}
}
