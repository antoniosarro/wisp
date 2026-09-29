// Package credential decides which paths hold credentials: keys, tokens,
// and secret files. Reading never asks, and what the model reads can leave
// through an approved fetch or command, so tools and the permission layer
// ask before reading these. It is a leaf package, shared by both.
package credential

import (
	"os"
	"path/filepath"
	"strings"
)

// dirs, under the home directory, hold keys and tokens.
var dirs = []string{
	".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker", ".password-store",
	".config/gcloud", ".config/gh", ".local/share/keyrings",
}

// files match key and secret files by name, anywhere.
var files = []string{
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*", "*.pem", "*.key", "*.p12", "*.pfx",
	"*.kdbx", ".netrc", ".pgpass", ".env", ".env.*",
}

// Path says whether path, symlinks followed, is a credential file or
// inside a credential directory. A path that can't be made absolute counts
// as a credential, so the caller asks.
func Path(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	abs = resolve(abs)
	if home, err := os.UserHomeDir(); err == nil {
		// Resolved too: when the home directory is behind a symlink
		// (/home -> /var/home), resolved paths would never match it.
		home = resolve(home)
		for _, dir := range dirs {
			if rel, err := filepath.Rel(filepath.Join(home, dir), abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return true
			}
		}
	}
	return Name(filepath.Base(abs))
}

// Name says whether a file name is a credential file's. Samples and
// templates (.env.example) hold placeholders, not secrets.
func Name(base string) bool {
	if strings.HasSuffix(base, ".example") || strings.HasSuffix(base, ".sample") || strings.HasSuffix(base, ".template") {
		return false
	}
	for _, pattern := range files {
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
	}
	return false
}

// resolve follows the symlinks of path's longest existing prefix, so a
// link to a file that doesn't exist yet still resolves through its
// directory. path must be absolute.
func resolve(path string) string {
	rest := ""
	for dir := path; ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		if filepath.Dir(dir) == dir {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}
