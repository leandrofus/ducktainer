package server

import (
	"net/http/httptest"
	"testing"
)

func TestServerCandidates(t *testing.T) {
	s := &Server{Listen: "127.0.0.1:9900"}
	req := httptest.NewRequest("GET", "http://localhost:9900/", nil)
	if c, loop := s.serverCandidates(req); !loop || len(c) != 0 {
		t.Fatalf("loopback only: %v %v", c, loop)
	}
	s.Listen = "10.1.2.3:9900"
	if c, loop := s.serverCandidates(req); loop || len(c) != 1 || c[0].URL != "http://10.1.2.3:9900" {
		t.Fatalf("specific address: %v %v", c, loop)
	}
	// the name the browser used counts when it is not loopback; the agents-only port is offered too
	s.AgentListen = "10.1.2.3:9901"
	req = httptest.NewRequest("GET", "http://srv.lan:9900/", nil)
	c, _ := s.serverCandidates(req)
	if len(c) != 3 || c[0].URL != "http://srv.lan:9900" || c[2].URL != "http://10.1.2.3:9901" {
		t.Fatalf("candidates: %v", c)
	}
}
