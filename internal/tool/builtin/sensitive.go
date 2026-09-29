package builtin

import (
	"os"
	"path/filepath"
	"strings"
)

// Reading never asks, and what the model reads can leave through an
// approved fetch or command: so reading credentials asks first. read and
// grep ask for a credential path given directly (RiskyCall); a wider walk
// skips credential directories (walkFiles) and grep skips credential files
// (credentialFile), so a search of the project can't return a .env.

// credentialDirs, under the home directory, hold keys and tokens.
var credentialDirs = []string{
	".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker", ".password-store",
	".config/gcloud", ".config/gh", ".local/share/keyrings",
}

// credentialFiles match key and secret files by name, anywhere.
var credentialFiles = []string{
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*", "*.pem", "*.key", "*.p12", "*.pfx",
	"*.kdbx", ".netrc", ".pgpass", ".env", ".env.*",
}

// sensitivePath says whether path, symlinks followed, is a credential
// file or inside a credential directory. A path that can't be resolved
// counts as sensitive, so the call asks.
func sensitivePath(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range credentialDirs {
			if rel, err := filepath.Rel(filepath.Join(home, dir), abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return true
			}
		}
	}
	return credentialName(filepath.Base(abs))
}

// credentialName says whether a file name is a credential file's. Samples
// and templates (.env.example) hold placeholders, not secrets.
func credentialName(base string) bool {
	if strings.HasSuffix(base, ".example") || strings.HasSuffix(base, ".sample") || strings.HasSuffix(base, ".template") {
		return false
	}
	for _, pattern := range credentialFiles {
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
	}
	return false
}

// credentialFile is sensitivePath for a file met in a walk, which already
// skipped credential directories: its name decides, or, for a symlink,
// where it leads. Cheaper than sensitivePath, which resolves every file's
// full path.
func credentialFile(path string) bool {
	if credentialName(filepath.Base(path)) {
		return true
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0 && sensitivePath(path)
}
