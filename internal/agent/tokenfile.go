package agent

import (
	"fmt"
	"os"
	"strings"
)

// ReadTokenFile reads the agent token from a file: either the
// DUCKTAINER_AGENT_TOKEN=... line the installers write (the same file systemd
// reads as an environment file) or just the token. Windows services have no
// environment file, so they read the token from here.
func ReadTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "DUCKTAINER_AGENT_TOKEN=")), nil
	}
	return "", fmt.Errorf("%s holds no token", path)
}
