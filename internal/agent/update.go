package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	updateFirstCheck = 30 * time.Second
	updateEvery      = 10 * time.Minute
	updateRetry      = time.Hour // a version that failed is not tried again for this long
)

// versionInfo is what the server answers to /api/agent/version.
type versionInfo struct {
	Version string `json:"version"`
	Auto    bool   `json:"auto"` // the host is set to update by itself
}

// updatable says whether this build can replace itself: release builds only
// (a "dev" binary has no version to compare), and not on Windows, where a
// running executable cannot be replaced.
func updatable(version string) bool {
	return version != "" && version != "dev" && runtime.GOOS != "windows"
}

// autoUpdate keeps the agent at the version of the server it connects to:
// it asks the server which version it runs and, when it differs and the host
// allows it, downloads the new binary, checks that it runs here and restarts
// into it. A binary that does not run, or reports another version, leaves the
// current one untouched.
func autoUpdate(ctx context.Context, cfg Config, tlsConf *tls.Config) {
	if !updatable(cfg.Version) {
		return
	}
	client := &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{TLSClientConfig: tlsConf}}
	base := httpBase(cfg.Server)
	var failed string
	var failedAt time.Time
	wait := updateFirstCheck
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = updateEvery
		info, err := fetchVersion(ctx, client, base, cfg)
		if err != nil {
			log.Printf("[agent] update check: %v", err)
			continue
		}
		if !info.Auto || info.Version == "" || info.Version == "dev" || info.Version == cfg.Version {
			continue
		}
		if info.Version == failed && time.Since(failedAt) < updateRetry {
			continue
		}
		log.Printf("[agent] the server runs %s and this agent %s: updating", info.Version, cfg.Version)
		exe, err := selfPath()
		if err == nil {
			err = replaceSelf(ctx, client, base, cfg, info.Version, exe)
		}
		if err != nil {
			log.Printf("[agent] update to %s failed, staying on %s: %v", info.Version, cfg.Version, err)
			failed, failedAt = info.Version, time.Now()
		}
	}
}

// httpBase turns the --server value into the base of its HTTP API.
func httpBase(server string) string {
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	u, err := url.Parse(server)
	if err != nil {
		return strings.TrimRight(server, "/")
	}
	scheme := "https"
	if u.Scheme == "http" || u.Scheme == "ws" {
		scheme = "http"
	}
	return scheme + "://" + u.Host + strings.TrimRight(u.Path, "/")
}

func authedGet(ctx context.Context, client *http.Client, cfg Config, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("X-Agent-Name", cfg.Name)
	req.Header.Set("X-Agent-Version", cfg.Version)
	return client.Do(req)
}

func fetchVersion(ctx context.Context, client *http.Client, base string, cfg Config) (versionInfo, error) {
	var info versionInfo
	resp, err := authedGet(ctx, client, cfg, base+"/api/agent/version")
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("the server answered %s", resp.Status)
	}
	return info, json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&info)
}

// selfPath is the file this agent runs from.
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// replaceSelf downloads the server's binary, proves it runs and reports the
// expected version, swaps it in over exe and re-executes into it.
func replaceSelf(ctx context.Context, client *http.Client, base string, cfg Config, want, exe string) error {
	tmp := exe + ".new"
	defer os.Remove(tmp) // gone after the rename; cleans up after a failure
	resp, err := authedGet(ctx, client, cfg, base+"/api/agent/binary?arch="+runtime.GOARCH)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: the server answered %s", resp.Status)
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, tmp, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the new binary does not run here: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if got := strings.TrimSpace(string(out)); got != "ducktainer "+want {
		return fmt.Errorf("the new binary reports %q, expected version %s", got, want)
	}
	if err := os.Rename(tmp, exe); err != nil {
		return err
	}
	log.Printf("[agent] updated to %s: restarting", want)
	return execSelf(exe) // only returns on failure
}
