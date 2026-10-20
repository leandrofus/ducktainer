package compose

import (
	"archive/tar"
	"bytes"
	"context"
	"ducktainer/internal/i18n"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ducktainer/internal/config"
	"ducktainer/internal/docker"
	"ducktainer/internal/dockersock"
)

// FS reads and writes stack files where they live: on this machine for the
// local endpoint, or on the remote node through a helper container.
type FS interface {
	ReadFile(ctx context.Context, path string) ([]byte, error)
	// WriteFile keeps the mode and owner of an existing file; new files get
	// def and the owner of their directory (created if missing).
	WriteFile(ctx context.Context, path string, data []byte, def os.FileMode) error
	Exists(ctx context.Context, path string) (bool, error)
	Rename(ctx context.Context, from, to string) error
	Remove(ctx context.Context, path string) error
}

// LocalFS is the filesystem of the machine running ducktainer.
type LocalFS struct{}

func (LocalFS) ReadFile(_ context.Context, p string) ([]byte, error) { return os.ReadFile(p) }

func (LocalFS) WriteFile(_ context.Context, p string, data []byte, def os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	mode := def
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}
	return os.WriteFile(p, data, mode)
}

func (LocalFS) Exists(_ context.Context, p string) (bool, error) {
	_, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (LocalFS) Rename(_ context.Context, from, to string) error { return os.Rename(from, to) }

func (LocalFS) Remove(_ context.Context, p string) error {
	err := os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// DefaultComposeImage provides the docker CLI with the compose plugin.
const DefaultComposeImage = "docker:cli"

const (
	helperLabel = "ducktainer.helper"
	hostRoot    = "/host" // the node's / inside the files helper
)

func composeImage(ep config.Endpoint) string {
	if ep.ComposeImage != "" {
		return ep.ComposeImage
	}
	return DefaultComposeImage
}

func dockerSocket(ep config.Endpoint) string {
	if ep.DockerSocket != "" {
		return ep.DockerSocket
	}
	// a local unix endpoint: the socket the daemon really listens on
	if u, err := url.Parse(ep.URL); err == nil && u.Scheme == "unix" {
		p := u.Path
		if p == "" {
			p = u.Host
		}
		if dockersock.IsSocket(p) {
			return p
		}
		if alt := dockersock.Find(); alt != "" {
			return alt
		}
	}
	return dockersock.Default
}

// NodeFS reaches the node's filesystem through a long-lived helper container
// (ducktainer-files) that mounts / at /host: files are read and written with
// the archive API and moved with exec. It exits by itself after an hour idle
// and is recreated on demand.
type NodeFS struct {
	C    *docker.Client
	Auth func(image string) string // X-Registry-Auth for pulling the image

	mu      sync.Mutex
	touched time.Time
}

var (
	nodeFSMu sync.Mutex
	nodeFSes = map[string]*NodeFS{}
)

// ForNode returns the shared NodeFS of an endpoint.
func ForNode(c *docker.Client, auth func(string) string) *NodeFS {
	nodeFSMu.Lock()
	defer nodeFSMu.Unlock()
	key := c.Endpoint.Name + "\x00" + c.Endpoint.URL
	n := nodeFSes[key]
	if n == nil {
		n = &NodeFS{C: c, Auth: auth}
		nodeFSes[key] = n
	}
	n.C, n.Auth = c, auth
	return n
}

const filesHelper = "ducktainer-files"

// helper returns the id of the running files helper, creating it if needed.
func (n *NodeFS) helper(ctx context.Context) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	var in struct {
		ID    string `json:"Id"`
		State struct{ Running bool }
	}
	if err := n.C.Get(ctx, "/containers/"+filesHelper+"/json", nil, &in); err == nil {
		if in.State.Running {
			// the idle timer restarts with use
			if time.Since(n.touched) > 5*time.Minute {
				if _, _, err := n.exec(ctx, in.ID, "touch", "/tmp/used"); err == nil {
					n.touched = time.Now()
				}
			}
			return in.ID, nil
		}
		_ = n.C.JSON(ctx, http.MethodDelete, "/containers/"+in.ID, url.Values{"force": {"1"}}, nil, nil)
	} else if !docker.IsNotFound(err) {
		return "", err
	}
	image := composeImage(n.C.Endpoint)
	if err := EnsureImage(ctx, n.C, image, n.Auth, io.Discard); err != nil {
		return "", err
	}
	// Leaves when /tmp/used has not been touched for an hour.
	idle := `touch /tmp/used; while [ $(( $(date +%s) - $(stat -c %Y /tmp/used) )) -lt 3600 ]; do sleep 30; done`
	body := map[string]any{
		"Image":      image,
		"Entrypoint": []string{"sh", "-c", idle},
		"Labels":     map[string]string{helperLabel: "files"},
		"HostConfig": map[string]any{
			"Binds":       []string{"/:" + hostRoot},
			"AutoRemove":  true,
			"SecurityOpt": []string{"label=disable"},
		},
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := n.C.JSON(ctx, http.MethodPost, "/containers/create", url.Values{"name": {filesHelper}}, body, &created); err != nil {
		return "", i18n.Errorf("cannot create the files helper on %s: %v", n.C.Endpoint.Name, err)
	}
	if err := n.C.JSON(ctx, http.MethodPost, "/containers/"+created.ID+"/start", nil, nil, nil); err != nil {
		return "", err
	}
	n.touched = time.Now()
	return created.ID, nil
}

// exec runs cmd in container id and returns its combined output.
func (n *NodeFS) exec(ctx context.Context, id string, cmd ...string) (string, int, error) {
	var created struct {
		ID string `json:"Id"`
	}
	if err := n.C.JSON(ctx, http.MethodPost, "/containers/"+id+"/exec", nil, map[string]any{"Cmd": cmd, "AttachStdout": true, "AttachStderr": true, "Tty": true}, &created); err != nil {
		return "", -1, err
	}
	conn, br, err := n.C.Hijack(ctx, http.MethodPost, "/exec/"+created.ID+"/start", map[string]any{"Detach": false, "Tty": true})
	if err != nil {
		return "", -1, err
	}
	out, _ := io.ReadAll(io.LimitReader(br, 1<<20))
	conn.Close()
	var in struct{ ExitCode int }
	if err := n.C.Get(ctx, "/exec/"+created.ID+"/json", nil, &in); err != nil {
		return "", -1, err
	}
	return strings.TrimSpace(string(out)), in.ExitCode, nil
}

// run executes a shell command in the helper and fails on a non-zero exit.
func (n *NodeFS) run(ctx context.Context, cmd ...string) (string, error) {
	id, err := n.helper(ctx)
	if err != nil {
		return "", err
	}
	out, code, err := n.exec(ctx, id, cmd...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return out, fmt.Errorf("%s: %s", n.C.Endpoint.Name, out)
	}
	return out, nil
}

func hostPath(p string) (string, error) {
	if !path.IsAbs(p) {
		return "", i18n.Errorf("%s is not an absolute path on the node", p)
	}
	return hostRoot + path.Clean(p), nil
}

// get returns the tar header and content of a file.
func (n *NodeFS) get(ctx context.Context, p string) (*tar.Header, []byte, error) {
	hp, err := hostPath(p)
	if err != nil {
		return nil, nil, err
	}
	id, err := n.helper(ctx)
	if err != nil {
		return nil, nil, err
	}
	resp, err := n.C.Do(ctx, http.MethodGet, "/containers/"+id+"/archive", url.Values{"path": {hp}}, nil, nil)
	if err != nil {
		if docker.IsNotFound(err) {
			return nil, nil, &fs.PathError{Op: "read", Path: p, Err: fs.ErrNotExist}
		}
		return nil, nil, err
	}
	defer resp.Body.Close()
	tr := tar.NewReader(resp.Body)
	h, err := tr.Next()
	if err != nil {
		return nil, nil, err
	}
	if h.Typeflag == tar.TypeDir {
		return nil, nil, i18n.Errorf("%s is a directory", p)
	}
	if h.Typeflag == tar.TypeSymlink {
		target := h.Linkname
		if !path.IsAbs(target) {
			target = path.Join(path.Dir(p), target)
		}
		return n.get(ctx, target)
	}
	data, err := io.ReadAll(io.LimitReader(tr, 16<<20))
	return h, data, err
}

func (n *NodeFS) ReadFile(ctx context.Context, p string) ([]byte, error) {
	_, data, err := n.get(ctx, p)
	return data, err
}

func (n *NodeFS) Exists(ctx context.Context, p string) (bool, error) {
	hp, err := hostPath(p)
	if err != nil {
		return false, err
	}
	id, err := n.helper(ctx)
	if err != nil {
		return false, err
	}
	resp, err := n.C.Do(ctx, http.MethodHead, "/containers/"+id+"/archive", url.Values{"path": {hp}}, nil, nil)
	if err != nil {
		if docker.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	resp.Body.Close()
	return true, nil
}

func (n *NodeFS) WriteFile(ctx context.Context, p string, data []byte, def os.FileMode) error {
	hp, err := hostPath(p)
	if err != nil {
		return err
	}
	h := &tar.Header{Name: path.Base(p), Mode: int64(def), Typeflag: tar.TypeReg}
	if old, _, err := n.get(ctx, p); err == nil {
		h.Mode, h.Uid, h.Gid = old.Mode&0o7777, old.Uid, old.Gid
	} else if errors.Is(err, fs.ErrNotExist) {
		// create missing directories owned like their closest existing parent
		dir := path.Dir(hp)
		const mkdir = `d=$1; a=$d; while [ ! -e "$a" ]; do a=$(dirname "$a"); done; o=$(stat -c %u:%g "$a")
while [ "$d" != "$a" ]; do set -- "$d" "$@"; d=$(dirname "$d"); done
for x in "$@"; do mkdir -p "$x" && chown "$o" "$x" || exit 1; done`
		if _, err := n.run(ctx, "sh", "-c", mkdir, "-", dir); err != nil {
			return err
		}
		owner, err := n.run(ctx, "stat", "-c", "%u %g", dir)
		if err != nil {
			return err
		}
		fmt.Sscan(owner, &h.Uid, &h.Gid)
	} else {
		return err
	}
	h.Size = int64(len(data))
	h.ModTime = time.Now()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	tw.Write(data)
	tw.Close()
	id, err := n.helper(ctx)
	if err != nil {
		return err
	}
	resp, err := n.C.Do(ctx, http.MethodPut, "/containers/"+id+"/archive", url.Values{"path": {path.Dir(hp)}, "copyUIDGID": {"1"}}, &buf, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (n *NodeFS) Rename(ctx context.Context, from, to string) error {
	hf, err := hostPath(from)
	if err != nil {
		return err
	}
	ht, err := hostPath(to)
	if err != nil {
		return err
	}
	_, err = n.run(ctx, "mv", "-f", hf, ht)
	return err
}

func (n *NodeFS) Remove(ctx context.Context, p string) error {
	hp, err := hostPath(p)
	if err != nil {
		return err
	}
	_, err = n.run(ctx, "rm", "-f", hp)
	return err
}

// EnsureImage pulls image on the endpoint when it is missing.
func EnsureImage(ctx context.Context, c *docker.Client, image string, auth func(string) string, w io.Writer) error {
	if err := c.Get(ctx, "/images/"+image+"/json", nil, nil); err == nil {
		return nil
	} else if !docker.IsNotFound(err) {
		return err
	}
	fmt.Fprintf(w, "$ docker pull %s\n", image)
	var hdr http.Header
	if auth != nil {
		hdr = http.Header{"X-Registry-Auth": {auth(image)}}
	}
	resp, err := c.Do(ctx, http.MethodPost, "/images/create", url.Values{"fromImage": {image}}, nil, hdr)
	if err != nil {
		return i18n.Errorf("cannot pull %s on %s: %v", image, c.Endpoint.Name, err)
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var m struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&m); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if m.Error != "" {
			return i18n.Errorf("cannot pull %s on %s: %v", image, c.Endpoint.Name, m.Error)
		}
	}
}

// runOnNode runs `docker compose` in a throwaway container on the node,
// with the daemon socket and the project directories mounted at the same
// paths, so relative volumes, env_file, build contexts and .env resolve on
// the node exactly as if compose were run there.
func (r *Runner) runOnNode(ctx context.Context, stdout, stderr io.Writer, dir string, dirs []string, cmd []string, vars ...string) error {
	c := r.Client
	image := composeImage(r.Endpoint)
	if err := EnsureImage(ctx, c, image, r.Auth, stderr); err != nil {
		return err
	}
	sock := dockerSocket(r.Endpoint)
	binds := []string{sock + ":/var/run/docker.sock"}
	seen := map[string]bool{}
	for _, d := range append([]string{dir}, dirs...) {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		binds = append(binds, d+":"+d)
	}
	body := map[string]any{
		"Image":      image,
		"Entrypoint": cmd,
		"WorkingDir": dir,
		"Env":        append(vars, "DOCKER_HOST=unix:///var/run/docker.sock", "DOCKER_CONFIG=/ducktainer-config"),
		"Labels":     map[string]string{helperLabel: "compose"},
		"HostConfig": map[string]any{
			"Binds":       binds,
			"SecurityOpt": []string{"label=disable"},
		},
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.JSON(ctx, http.MethodPost, "/containers/create", nil, body, &created); err != nil {
		return i18n.Errorf("cannot run compose on %s: %v", r.Endpoint.Name, err)
	}
	id := created.ID
	defer c.JSON(context.WithoutCancel(ctx), http.MethodDelete, "/containers/"+id, url.Values{"force": {"1"}}, nil, nil)

	// registries configured in ducktainer
	cfg, _ := json.Marshal(map[string]any{"auths": r.auths()})
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "ducktainer-config/", Typeflag: tar.TypeDir, Mode: 0o700})
	tw.WriteHeader(&tar.Header{Name: "ducktainer-config/config.json", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(cfg))})
	tw.Write(cfg)
	tw.Close()
	if resp, err := c.Do(ctx, http.MethodPut, "/containers/"+id+"/archive", url.Values{"path": {"/"}}, &buf, nil); err == nil {
		resp.Body.Close()
	} else {
		return err
	}

	if err := c.JSON(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil, nil); err != nil {
		if strings.Contains(err.Error(), sock) {
			return i18n.Errorf("the Docker socket %s does not exist on %s: set the right one in the host settings", sock, r.Endpoint.Name)
		}
		return err
	}
	resp, err := c.Do(ctx, http.MethodGet, "/containers/"+id+"/logs", url.Values{"follow": {"1"}, "stdout": {"1"}, "stderr": {"1"}}, nil, nil)
	if err != nil {
		return err
	}
	_ = docker.Demux(resp.Body, func(stream byte, p []byte) error {
		if stream == 2 {
			_, err := stderr.Write(p)
			return err
		}
		_, err := stdout.Write(p)
		return err
	})
	resp.Body.Close()
	var res struct{ StatusCode int }
	if err := c.JSON(ctx, http.MethodPost, "/containers/"+id+"/wait", nil, nil, &res); err != nil {
		return err
	}
	if res.StatusCode != 0 {
		return fmt.Errorf("exit status %d", res.StatusCode)
	}
	return nil
}
