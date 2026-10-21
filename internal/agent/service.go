package agent

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	envDir  = "/etc/ducktainer-agent"
	envFile = envDir + "/agent.env"
)

// quote protects a value in a systemd ExecStart line.
func quote(v string) string { return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"` }

// InstallService installs the agent as a service: a systemd unit on Linux,
// a Windows service on Windows.
func InstallService(cfg Config) error {
	if runtime.GOOS == "windows" {
		return installWindowsService(cfg)
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("the agent service can only be installed on Linux (systemd) and Windows")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("root privileges required to install systemd service (use sudo)")
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}

	destBin := "/usr/local/bin/ducktainer-agent"
	if self != destBin {
		data, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if err := os.WriteFile(destBin+".new", data, 0o755); err != nil {
			return err
		}
		if err := os.Rename(destBin+".new", destBin); err != nil {
			return err
		}
	}

	// The token stays in a root-only file, not in the unit or on `ps`.
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(envFile, []byte("DUCKTAINER_AGENT_TOKEN="+cfg.Token+"\n"), 0o600); err != nil {
		return err
	}

	var args []string
	args = append(args, "agent", "--server", quote(cfg.Server), "--name", quote(cfg.Name))
	if cfg.Socket != "" {
		args = append(args, "--socket", quote(cfg.Socket))
	}
	if cfg.Insecure {
		args = append(args, "--insecure")
	}
	if cfg.Fingerprint != "" {
		args = append(args, "--fingerprint", cfg.Fingerprint)
	}

	execLine := destBin + " " + strings.Join(args, " ")

	unitContent := fmt.Sprintf(`[Unit]
Description=ducktainer agent for Docker
Documentation=https://github.com/leandrofus/ducktainer
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
EnvironmentFile=%s
ExecStart=%s
Restart=always
RestartSec=5s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`, envFile, execLine)

	unitPath := "/etc/systemd/system/ducktainer-agent.service"
	if err := os.WriteFile(unitPath, []byte(unitContent), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", unitPath, err)
	}

	_ = exec.Command("systemctl", "daemon-reload").Run()
	if err := exec.Command("systemctl", "enable", "--now", "ducktainer-agent").Run(); err != nil {
		return fmt.Errorf("failed to enable and start ducktainer-agent: %w", err)
	}

	fmt.Println("ducktainer-agent service installed and started successfully! ✓")
	fmt.Printf("Service unit: %s\n", unitPath)
	fmt.Println("Check status with: systemctl status ducktainer-agent")
	return nil
}
