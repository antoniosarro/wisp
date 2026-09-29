package builtin

import (
	"os"
	"path/filepath"

	"github.com/antoniosarro/wisp/internal/credential"
)

// Reading never asks, and what the model reads can leave through an
// approved fetch or command: so reading credentials asks first. read and
// grep ask for a credential path given directly (RiskyCall); a wider walk
// skips credential directories (walkFiles) and grep skips credential files
// (credentialFile), so a search of the project can't return a .env. Which
// paths are credentials is the credential package's call.

// sensitivePath says whether path is a credential (credential.Path).
func sensitivePath(path string) bool { return credential.Path(path) }

// credentialFile is sensitivePath for a file met in a walk, which already
// skipped credential directories: its name decides, or, for a symlink,
// where it leads. Cheaper than sensitivePath, which resolves every file's
// full path.
func credentialFile(path string) bool {
	if credential.Name(filepath.Base(path)) {
		return true
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0 && sensitivePath(path)
}
