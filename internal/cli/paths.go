package cli

import (
	"os"
	"path/filepath"
)

// Where wisp keeps its files: global config, state, and sessions under the
// XDG base directories, and a project's own config in its .wisp directory.

// repoURL is wisp's home, named to endpoints (OpenRouter app attribution).
const repoURL = "https://github.com/antoniosarro/wisp"

// xdgPath joins elem to $env/wisp, or to ~/fallback/wisp when env is unset.
func xdgPath(env, fallback string, elem ...string) (string, error) {
	dir := os.Getenv(env)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, fallback)
	}
	return filepath.Join(append([]string{dir, "wisp"}, elem...)...), nil
}

// statePath is $XDG_STATE_HOME/wisp/state.json, or ~/.local/state/..., or
// "" without a home directory.
func statePath() string {
	path, _ := xdgPath("XDG_STATE_HOME", filepath.Join(".local", "state"), "state.json")
	return path
}

// sessionDBPath is $XDG_DATA_HOME/wisp/session.db, or ~/.local/share/...:
// one database for every project, each session tagged with its directory.
func sessionDBPath() (string, error) {
	return xdgPath("XDG_DATA_HOME", filepath.Join(".local", "share"), "session.db")
}

// legacySessionDB is where versions before the shared database kept a
// project's sessions, relative to it; openStore moves them over.
const legacySessionDB = ".wisp/session.db"
