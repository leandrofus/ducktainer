//go:build windows

package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// ServiceName is the Windows service the agent runs as.
const ServiceName = "ducktainer-agent"

// DataDir holds the agent's token file and log (restricted to Administrators
// and SYSTEM).
func DataDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "ducktainer-agent")
}

// TokenFile is where the service reads the agent token from.
func TokenFile() string { return filepath.Join(DataDir(), "agent.env") }

// installWindowsService installs and starts the agent as an automatic Windows
// service (run from an elevated prompt): it copies this executable to Program
// Files, keeps the token in a file only Administrators and SYSTEM can read,
// and sets the service to restart after a failure or an update.
func installWindowsService(cfg Config) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	progs := os.Getenv("ProgramFiles")
	if progs == "" {
		progs = `C:\Program Files`
	}
	destDir := filepath.Join(progs, "ducktainer-agent")
	dest := filepath.Join(destDir, "ducktainer-agent.exe")

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("cannot reach the service manager (run from an elevated prompt as Administrator): %w", err)
	}
	defer m.Disconnect()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(DataDir(), 0o700); err != nil {
		return err
	}
	if err := restrictDir(DataDir()); err != nil {
		return fmt.Errorf("could not restrict %s: %w", DataDir(), err)
	}
	if err := os.WriteFile(TokenFile(), []byte("DUCKTAINER_AGENT_TOKEN="+cfg.Token+"\r\n"), 0o600); err != nil {
		return err
	}

	args := []string{"agent", "--server", cfg.Server, "--name", cfg.Name, "--token-file", TokenFile()}
	if cfg.Socket != "" {
		args = append(args, "--socket", cfg.Socket)
	}
	if cfg.Insecure {
		args = append(args, "--insecure")
	}
	if cfg.Fingerprint != "" {
		args = append(args, "--fingerprint", cfg.Fingerprint)
	}

	// an agent that is already installed runs from dest: stop it to replace the file
	existing, err := m.OpenService(ServiceName)
	if err == nil {
		stopService(existing)
	}
	if self != dest {
		if err := copyExecutable(self, dest); err != nil {
			if existing != nil {
				existing.Close()
			}
			return err
		}
	}

	scfg := mgr.Config{
		DisplayName:      "ducktainer agent",
		Description:      "Connects this machine's Docker engine to a ducktainer server (outbound tunnel).",
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
	}
	var s *mgr.Service
	if existing != nil {
		s = existing
		scfg.BinaryPathName = commandLine(dest, args)
		cur, cerr := s.Config()
		if cerr != nil {
			s.Close()
			return cerr
		}
		cur.BinaryPathName, cur.StartType, cur.DisplayName, cur.Description = scfg.BinaryPathName, scfg.StartType, scfg.DisplayName, scfg.Description
		cur.DelayedAutoStart = true
		if err := s.UpdateConfig(cur); err != nil {
			s.Close()
			return err
		}
	} else {
		if s, err = m.CreateService(ServiceName, dest, scfg, args...); err != nil {
			return err
		}
	}
	defer s.Close()

	restart := mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: 5 * time.Second}
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{restart, restart, restart}, 24*60*60); err == nil {
		s.SetRecoveryActionsOnNonCrashFailures(true) // an update exits with an error on purpose
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("the service was installed but did not start: %w", err)
	}
	fmt.Println("ducktainer-agent service installed and started successfully! ✓")
	fmt.Printf("Executable: %s\nToken file: %s\nLog: %s\n", dest, TokenFile(), filepath.Join(DataDir(), "agent.log"))
	fmt.Println("Check it with: Get-Service ducktainer-agent")
	return nil
}

func commandLine(exe string, args []string) string {
	line := windows.EscapeArg(exe)
	for _, a := range args {
		line += " " + windows.EscapeArg(a)
	}
	return line
}

func stopService(s *mgr.Service) {
	if st, err := s.Control(svc.Stop); err == nil {
		for i := 0; i < 50 && st.State != svc.Stopped; i++ {
			time.Sleep(200 * time.Millisecond)
			if st, err = s.Query(); err != nil {
				return
			}
		}
	}
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	os.Remove(dst + ".old")
	if _, err := os.Stat(dst); err == nil {
		os.Rename(dst, dst+".old") // a copy still in use cannot be deleted, but can be moved aside
	}
	return os.Rename(tmp, dst)
}

// restrictDir leaves dir to SYSTEM and Administrators.
func restrictDir(dir string) error {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
