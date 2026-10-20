// Package docker is a small Docker Engine API client. It speaks HTTP directly
// over a unix socket, TCP(+TLS) to a `ducktainer expose` proxy, or SSH using
// `docker system dial-stdio` on the remote host, so nothing has to be
// installed there besides Docker itself.
package docker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"ducktainer/internal/i18n"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ducktainer/internal/agent"
	"ducktainer/internal/config"
	"ducktainer/internal/dockersock"
)

type Client struct {
	Endpoint config.Endpoint
	dial     func(ctx context.Context) (net.Conn, error)
	http     *http.Client // no timeout: used for streams too
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

func New(ep config.Endpoint) (*Client, error) {
	u, err := url.Parse(ep.URL)
	if err != nil {
		return nil, i18n.Errorf("endpoint %s: %v", ep.Name, err)
	}
	c := &Client{Endpoint: ep}
	switch u.Scheme {
	case "unix":
		path := u.Path
		if path == "" {
			path = u.Host
		}
		c.dial = func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "unix", path)
			if errors.Is(err, os.ErrNotExist) {
				// a saved default that no longer exists (Docker replaced by Podman)
				if alt := dockersock.Find(); alt != "" && alt != path {
					conn, err = d.DialContext(ctx, "unix", alt)
					path = alt
				}
			}
			if err != nil {
				return nil, permissionHint(path, err)
			}
			return conn, nil
		}
	case "tcp", "http", "https":
		addr := u.Host
		useTLS := ep.TLS || u.Scheme == "https"
		c.dial = func(ctx context.Context) (net.Conn, error) {
			d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil || !useTLS {
				return conn, err
			}
			tc := tls.Client(conn, tlsConfig(ep.Fingerprint, u.Hostname()))
			if err := tc.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			return tc, nil
		}
	case "ssh":
		c.dial = sshDialer(u, ep.SSHKey)
	case "npipe":
		pipe := PipePath(u)
		c.dial = func(ctx context.Context) (net.Conn, error) { return dialPipe(ctx, pipe) }
	case "wsl":
		distro := u.Host
		if distro == "" {
			distro = strings.Trim(u.Path, "/")
		}
		c.dial = wslDialer(distro)
	case "agent":
		name := u.Host
		if name == "" {
			name = u.Path
		}
		name = strings.TrimPrefix(name, "/")
		if name == "" {
			name = ep.Name
		}
		c.dial = func(ctx context.Context) (net.Conn, error) {
			return agent.DefaultHub.Dial(ctx, name)
		}
	default:
		return nil, i18n.Errorf("endpoint %s: unsupported scheme %q (use unix://, ssh://, tcp://, agent://, npipe:// or wsl://)", ep.Name, u.Scheme)
	}
	c.http = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return c.dial(ctx)
		},
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     60 * time.Second,
		DisableCompression:  true,
	}}
	return c, nil
}

func tlsConfig(fingerprint, serverName string) *tls.Config {
	want := strings.ToLower(strings.ReplaceAll(fingerprint, ":", ""))
	return &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true, // verified below by pinning, self-signed certs are expected
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if want == "" || len(raw) == 0 {
				return nil
			}
			sum := sha256.Sum256(raw[0])
			if hex.EncodeToString(sum[:]) != want {
				return i18n.Errorf("the server TLS fingerprint does not match the configured one")
			}
			return nil
		},
	}
}

// Transport exposes the underlying round tripper (used by the API proxy).
func (c *Client) Transport() http.RoundTripper { return c.http.Transport }

// Dial opens a raw connection to the daemon.
func (c *Client) Dial(ctx context.Context) (net.Conn, error) { return c.dial(ctx) }

func (c *Client) newRequest(ctx context.Context, method, path string, q url.Values, body any, hdr http.Header) (*http.Request, error) {
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case io.Reader:
		rd = b
		ctype = "application/x-tar"
	case []byte:
		rd = bytes.NewReader(b)
		ctype = "application/json"
	default:
		data, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(data)
		ctype = "application/json"
	}
	u := "http://docker" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	if c.Endpoint.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Endpoint.Token)
	}
	return req, nil
}

// Do performs a request and returns the response if the status is < 400.
// The caller must close the body.
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body any, hdr http.Header) (*http.Response, error) {
	req, err := c.newRequest(ctx, method, path, q, body, hdr)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker (%s): %w", c.Endpoint.Name, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var m struct {
			Message string `json:"message"`
		}
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &m) == nil && m.Message != "" {
			msg = m.Message
		}
		if msg == "" {
			msg = resp.Status
		}
		return nil, &APIError{Status: resp.StatusCode, Message: msg}
	}
	return resp, nil
}

// JSON performs a request and decodes the JSON response into out (if not nil).
func (c *Client) JSON(ctx context.Context, method, path string, q url.Values, body, out any) error {
	resp, err := c.Do(ctx, method, path, q, body, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return c.JSON(ctx, http.MethodGet, path, q, nil, out)
}

func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return c.JSON(ctx, http.MethodGet, "/_ping", nil, nil, nil)
}

// Hijack sends a request that upgrades the connection to a raw stream
// (exec start, attach). The returned reader must be used for reads.
func (c *Client) Hijack(ctx context.Context, method, path string, body any) (net.Conn, *bufio.Reader, error) {
	req, err := c.newRequest(ctx, method, path, nil, body, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		conn.Close()
		return nil, nil, &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(data))}
	}
	return conn, br, nil
}

// Demux reads a docker multiplexed stream (non-TTY logs/attach) and calls fn
// for each frame with stream 1=stdout, 2=stderr.
func Demux(r io.Reader, fn func(stream byte, p []byte) error) error {
	hdr := make([]byte, 8)
	buf := make([]byte, 32<<10)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return io.EOF
			}
			return err
		}
		n := int(binary.BigEndian.Uint32(hdr[4:]))
		if n > len(buf) {
			buf = make([]byte, n)
		}
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return err
		}
		if err := fn(hdr[0], buf[:n]); err != nil {
			return err
		}
	}
}

// PeerCertPEM connects to a TLS tcp endpoint (enforcing the pinned
// fingerprint) and returns its certificate as PEM, plus whether the
// certificate is valid for the host name used, so the docker CLI can run
// with full verification against it.
func PeerCertPEM(ctx context.Context, ep config.Endpoint) ([]byte, bool, error) {
	u, err := url.Parse(ep.URL)
	if err != nil {
		return nil, false, err
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: tlsConfig(ep.Fingerprint, u.Hostname())}
	conn, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, false, i18n.Errorf("the server presented no certificate")
	}
	leaf := certs[0]
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	return block, leaf.VerifyHostname(u.Hostname()) == nil, nil
}

// Secure checks a tcp endpoint before it is saved: off the local machine it
// must use TLS (the token would travel in clear otherwise), and a missing
// fingerprint is filled in from the certificate presented now, so later
// connections are pinned to it (trust on first use).
func Secure(ctx context.Context, ep config.Endpoint) (config.Endpoint, error) {
	u, err := url.Parse(ep.URL)
	if err != nil || (u.Scheme != "tcp" && u.Scheme != "http" && u.Scheme != "https") {
		return ep, err
	}
	useTLS := ep.TLS || u.Scheme == "https"
	if !useTLS {
		if ip := net.ParseIP(u.Hostname()); u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return ep, i18n.Errorf("%s: a remote tcp host needs TLS (the token would travel in clear); `ducktainer expose` enables it by default", ep.Name)
		}
		return ep, nil
	}
	if ep.Fingerprint != "" {
		return ep, nil
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: tlsConfig("", u.Hostname())}
	conn, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return ep, i18n.Errorf("%s: cannot read the TLS certificate to pin it: %v", ep.Name, err)
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return ep, i18n.Errorf("the server presented no certificate")
	}
	sum := sha256.Sum256(certs[0].Raw)
	ep.TLS, ep.Fingerprint = true, hex.EncodeToString(sum[:])
	return ep, nil
}
