package credential

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(dir, "keys")); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		filepath.Join(home, ".ssh", "config"):         true,
		filepath.Join(home, ".ssh"):                   true,
		filepath.Join(home, ".config", "gh", "h.yml"): true,
		filepath.Join(dir, "keys", "id_x"):            true, // through a symlink, to a file yet to exist
		"deploy/server.pem":                           true,
		"id_ed25519":                                  true,
		".env":                                        true,
		".env.production":                             true,
		".env.example":                                false,
		"internal/tokencount/count.go":                false,
		filepath.Join(home, ".sshrc"):                 false,
		home:                                          false, // contains credentials, isn't one
	} {
		if got := Path(path); got != want {
			t.Errorf("Path(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestName(t *testing.T) {
	for name, want := range map[string]bool{
		".env": true, ".env.local": true, "id_rsa.pub": true, "tls.key": true, ".netrc": true,
		".env.sample": false, "monkey": false, "keys.go": false, "README.md": false,
	} {
		if got := Name(name); got != want {
			t.Errorf("Name(%s) = %v, want %v", name, got, want)
		}
	}
}

// With the home directory behind a symlink, as /home -> /var/home is on
// some distributions, its credential directories are still recognized.
func TestPathWithHomeBehindASymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	if err := os.MkdirAll(filepath.Join(real, ".aws"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, ".aws", "credentials"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(link, ".aws", "credentials"), filepath.Join(real, ".aws", "credentials")} {
		if !Path(path) {
			t.Errorf("Path(%s) = false, want true", path)
		}
	}
}
