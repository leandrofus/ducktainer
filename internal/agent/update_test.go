//go:build !windows

package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeServer serves a "binary" (a shell script) that reports version `says`.
func fakeServer(t *testing.T, says string) (*httptest.Server, *[]string) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RequestURI()+" "+r.Header.Get("X-Agent-Version"))
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/agent/version":
			w.Write([]byte(`{"version":"2.0.0","auto":true}`))
		case "/api/agent/binary":
			w.Write([]byte("#!/bin/sh\necho 'ducktainer " + says + "'\n"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func updateWith(t *testing.T, says string) (exe string, execd *string, err error) {
	srv, _ := fakeServer(t, says)
	exe = filepath.Join(t.TempDir(), "ducktainer-agent")
	if e := os.WriteFile(exe, []byte("old"), 0o755); e != nil {
		t.Fatal(e)
	}
	execd = new(string)
	old := execSelf
	execSelf = func(p string) error { *execd = p; return nil }
	t.Cleanup(func() { execSelf = old })
	cfg := Config{Token: "tok", Name: "n", Version: "1.0.0"}
	err = replaceSelf(context.Background(), srv.Client(), srv.URL, cfg, "2.0.0", exe)
	return exe, execd, err
}

func TestReplaceSelfSwapsAndRestarts(t *testing.T) {
	exe, execd, err := updateWith(t, "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "ducktainer 2.0.0") {
		t.Fatalf("binary not replaced: %q", b)
	}
	if *execd != exe {
		t.Fatalf("did not re-exec into %s (got %q)", exe, *execd)
	}
	if _, err := os.Stat(exe + ".new"); err == nil {
		t.Fatal("temporary file left behind")
	}
}

func TestReplaceSelfKeepsTheBinaryWhenTheNewOneIsWrong(t *testing.T) {
	// the server's binary reports another version than the one it announced
	exe, execd, err := updateWith(t, "3.0.0")
	if err == nil || !strings.Contains(err.Error(), "expected version 2.0.0") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatalf("the current binary was replaced: %q", b)
	}
	if *execd != "" || func() bool { _, e := os.Stat(exe + ".new"); return e == nil }() {
		t.Fatal("restarted or left a temporary file after a failed update")
	}
}

func TestUpdatable(t *testing.T) {
	if updatable("dev") || updatable("") || !updatable("1.7.1") {
		t.Fatal("only release builds update themselves")
	}
	if got := httpBase("wss://srv:9900/x/"); got != "https://srv:9900/x" {
		t.Fatalf("httpBase: %q", got)
	}
	if got := httpBase("http://10.0.0.1:9900"); got != "http://10.0.0.1:9900" {
		t.Fatalf("httpBase: %q", got)
	}
}
