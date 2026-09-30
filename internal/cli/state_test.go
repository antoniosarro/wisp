package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRememberModel(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	rememberModel("http://a/v1", "qwen")
	rememberModel("http://b/v1", "llama")
	if s := loadState(); s.LastModel["http://a/v1"] != "qwen" || s.LastModel["http://b/v1"] != "llama" {
		t.Fatalf("state = %+v", s)
	}
	if info, err := os.Stat(filepath.Dir(statePath())); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("state directory: %v; want mode 0700", err)
	}
}

// Other versions of wisp share the state file: saving it must keep what
// they recorded, such as trusted projects.
func TestSavingStateKeepsOtherFields(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"last_model":{},"trusted":{"/proj":"abc"},"latest_release":"v1.2.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rememberModel("http://a/v1", "qwen")
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"/proj": "abc"`, `"latest_release": "v1.2.3"`, `"qwen"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("state lost %s:\n%s", want, data)
		}
	}
}

// Global config is under $XDG_CONFIG_HOME/wisp, else ~/.config/wisp, on
// every system: the docs and the home-manager module put it there, where
// os.UserConfigDir would pick ~/Library/Application Support on macOS.
func TestConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := configPath("mcp.json"), filepath.Join(home, ".config", "wisp", "mcp.json"); got != want {
		t.Errorf("configPath = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	if got, want := configPath("agents"), filepath.Join("/cfg", "wisp", "agents"); got != want {
		t.Errorf("configPath with XDG_CONFIG_HOME = %q, want %q", got, want)
	}
}
