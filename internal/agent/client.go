package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ducktainer/internal/dockersock"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// Config configures the outbound agent connection.
type Config struct {
	Server      string // e.g. "https://ducktainer.example.com:9900"
	Token       string // authorization token
	Name        string // node name in ducktainer
	Socket      string // path to local docker socket (default "/var/run/docker.sock")
	Insecure    bool   // skip TLS certificate validation
	Fingerprint string // expected sha256 fingerprint of the server certificate
}

// Run runs the standalone agent, maintaining a persistent outbound tunnel to
// the central ducktainer server and proxying requests to the local Docker socket.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Server == "" {
		return errors.New("server address is required (--server)")
	}
	if cfg.Token == "" {
		return errors.New("agent token is required (DUCKTAINER_AGENT_TOKEN or --token)")
	}
	if cfg.Name == "" {
		h, err := os.Hostname()
		if err != nil || h == "" {
			h = "agent-node"
		}
		cfg.Name = h
	}

	sockPath := cfg.Socket
	if sockPath == "" {
		sockPath = dockersock.Path()
	} else {
		sockPath = strings.TrimPrefix(sockPath, "unix://")
	}

	// Verify local docker socket connectivity
	if testConn, err := net.DialTimeout("unix", sockPath, 2*time.Second); err != nil {
		log.Printf("[agent] warning: cannot connect to local Docker socket %s: %v", sockPath, err)
		log.Printf("[agent] ensure Docker is running and this user has permission to access it")
	} else {
		_ = testConn.Close()
		log.Printf("[agent] local Docker socket verified at %s ✓", sockPath)
	}

	wsURL, tlsConf, err := buildConnectionParams(cfg)
	if err != nil {
		return err
	}

	log.Printf("[agent] starting agent %q -> %s", cfg.Name, cfg.Server)

	backoff := 2 * time.Second
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := connectAndServe(ctx, wsURL, tlsConf, cfg, sockPath)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		log.Printf("[agent] disconnected (%v), reconnecting in %v...", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func buildConnectionParams(cfg Config) (string, *tls.Config, error) {
	srv := cfg.Server
	if !strings.Contains(srv, "://") {
		srv = "https://" + srv
	}
	u, err := url.Parse(srv)
	if err != nil {
		return "", nil, fmt.Errorf("invalid server URL: %w", err)
	}

	scheme := "wss"
	if u.Scheme == "http" || u.Scheme == "ws" {
		scheme = "ws"
	}

	tunnelPath := strings.TrimRight(u.Path, "/") + "/api/agent/tunnel"
	q := url.Values{}
	q.Set("name", cfg.Name) // the token goes in the Authorization header

	wsURL := fmt.Sprintf("%s://%s%s?%s", scheme, u.Host, tunnelPath, q.Encode())

	var tlsConf *tls.Config
	if scheme == "wss" {
		wantFP := strings.ToLower(strings.ReplaceAll(cfg.Fingerprint, ":", ""))
		tlsConf = &tls.Config{
			ServerName:         u.Hostname(),
			InsecureSkipVerify: cfg.Insecure || wantFP != "",
		}
		if wantFP != "" {
			tlsConf.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 {
					return errors.New("server presented no certificate")
				}
				sum := sha256.Sum256(raw[0])
				if hex.EncodeToString(sum[:]) != wantFP {
					return fmt.Errorf("server TLS fingerprint (%s) does not match expected (%s)", hex.EncodeToString(sum[:]), wantFP)
				}
				return nil
			}
		}
	}

	return wsURL, tlsConf, nil
}

func connectAndServe(ctx context.Context, wsURL string, tlsConf *tls.Config, cfg Config, sockPath string) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		TLSClientConfig:  tlsConf,
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+cfg.Token)
	header.Set("X-Agent-Name", cfg.Name)

	ws, resp, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("handshake failed with HTTP %d: %w", resp.StatusCode, err)
		}
		return err
	}
	defer ws.Close()

	log.Printf("[agent] connected successfully to central server as %q ✓", cfg.Name)

	wsConn := NewWSConn(ws)
	yamuxConf := yamux.DefaultConfig()
	yamuxConf.EnableKeepAlive = true
	yamuxConf.KeepAliveInterval = 15 * time.Second
	yamuxConf.LogOutput = io.Discard

	session, err := yamux.Client(wsConn, yamuxConf)
	if err != nil {
		return fmt.Errorf("yamux client error: %w", err)
	}
	defer session.Close()

	// Wait for streams from central server
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return fmt.Errorf("session ended: %w", err)
		}

		go handleStream(stream, sockPath)
	}
}

func handleStream(stream *yamux.Stream, sockPath string) {
	defer stream.Close()

	br := bufio.NewReader(stream)
	if head, _ := br.Peek(len("GET " + dialPath)); string(head) == "GET "+dialPath {
		serveDial(stream, br)
		return
	}

	dockerConn, err := net.Dial("unix", sockPath)
	if err != nil {
		// the engine may have moved (Podman socket activated later, rootless)
		if alt := dockersock.Find(); alt != "" && alt != sockPath {
			dockerConn, err = net.Dial("unix", alt)
		}
	}
	if err != nil {
		log.Printf("[agent] failed to dial local docker socket %s: %v", sockPath, err)
		return
	}
	defer dockerConn.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(dockerConn, br)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(stream, dockerConn)
		done <- struct{}{}
	}()

	<-done
}
