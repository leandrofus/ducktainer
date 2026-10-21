//go:build windows

package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// Windows lets a running executable be renamed but not overwritten: the old
// file is moved aside and the new one takes its name.
func TestSwapBinaryMovesTheOldOneAside(t *testing.T) {
	dir := t.TempDir()
	exe, tmp := filepath.Join(dir, "ducktainer-agent.exe"), filepath.Join(dir, "ducktainer-agent.exe.new")
	os.WriteFile(exe, []byte("old"), 0o755)
	os.WriteFile(tmp, []byte("new"), 0o755)
	if err := swapBinary(tmp, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("exe holds %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old" {
		t.Fatalf("old holds %q", b)
	}
	// a second update replaces the .old left by the first
	os.WriteFile(tmp, []byte("newer"), 0o755)
	if err := swapBinary(tmp, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "newer" {
		t.Fatalf("exe holds %q", b)
	}
}
