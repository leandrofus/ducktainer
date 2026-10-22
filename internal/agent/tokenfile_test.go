package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTokenFile(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"env":   "DUCKTAINER_AGENT_TOKEN=abc123\n",
		"raw":   "abc123",
		"crlf":  "# written by the installer\r\nDUCKTAINER_AGENT_TOKEN=abc123\r\n",
		"blank": "\n\n",
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o600)
		got, err := ReadTokenFile(p)
		if name == "blank" {
			if err == nil {
				t.Errorf("%s: no error for an empty file", name)
			}
			continue
		}
		if err != nil || got != "abc123" {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	if _, err := ReadTokenFile(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file accepted")
	}
}
