package dockersock

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestFindUsesPodmanRootlessSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv("XDG_RUNTIME_DIR", dir)
	sock := filepath.Join(dir, "podman", "podman.sock")
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !IsSocket(sock) {
		t.Fatal("IsSocket false for a listening socket")
	}
	found := false
	for _, c := range Candidates() {
		if c.Path == sock {
			found = true
		}
	}
	if !found {
		t.Fatalf("rootless Podman socket %s not among candidates", sock)
	}
}

func TestContainerHostWins(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "unix:///x/podman.sock")
	if c := Candidates()[0]; c.Path != "/x/podman.sock" {
		t.Fatalf("first candidate = %v", c)
	}
}
