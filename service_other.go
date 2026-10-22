//go:build !windows

package main

import "ducktainer/internal/agent"

func runService([]string) bool { return false }

func runAgentService(agent.Config) bool { return false }
