//go:build windows

package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"

	"ducktainer/internal/agent"
)

// runAgentService runs the agent under the Service Control Manager. It
// returns false when this process was not started by it.
func runAgentService(cfg agent.Config) bool {
	if ok, err := svc.IsWindowsService(); err != nil || !ok {
		return false
	}
	os.MkdirAll(agent.DataDir(), 0o700)
	logTo(filepath.Join(agent.DataDir(), "agent.log"))
	if err := svc.Run(agent.ServiceName, &agentService{cfg: cfg}); err != nil {
		log.Printf("service: %v", err)
		os.Exit(1)
	}
	return true
}

type agentService struct{ cfg agent.Config }

func (s *agentService) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- agent.Run(ctx, s.cfg) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-errc:
			log.Printf("agent: %v", err)
			return true, 1 // the service's recovery options restart it
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				return false, 0
			}
		}
	}
}
