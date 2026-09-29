package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

// Masked output of a call that can't simply be run again, such as a
// command, is saved to a spill file the stand-in names, so the model can
// read or grep it. Spill files live in the user's cache directory, not a
// shared temp directory another user could plant files in.

// idempotent tools return the current state when run again, so their
// masked output is best recovered by running them again, not spilled.
var idempotent = map[string]bool{"read": true, "grep": true, "glob": true, "ls": true, "todo": true}

// spillMarker precedes the spill file's path in a stand-in.
const spillMarker = "; full output in "

// spillMaxAge is how long spill files are kept. Older ones are pruned; a
// resumed session that still needs one writes it back (restoreSpills).
const spillMaxAge = 7 * 24 * time.Hour

// spillDir is the directory spill files are kept in: wisp/masked in the
// user's cache directory ($XDG_CACHE_HOME, usually ~/.cache), or, when
// there is none, a directory of the user's own in the temp directory.
func spillDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "wisp", "masked")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("wisp-masked-%d", os.Getuid()))
}

// spillPath is where masked output of a non-idempotent call is kept, named
// by its content so the same output always maps to the same file; "" for
// idempotent calls, which are run again instead.
func spillPath(call model.ToolCall, content string) string {
	if idempotent[call.Name] {
		return ""
	}
	sum := sha256.Sum256([]byte(content))
	return filepath.Join(spillDir(), hex.EncodeToString(sum[:8])+".txt")
}

// spillFrom returns the spill file a stand-in names, or "".
func spillFrom(stub string) string {
	_, rest, ok := strings.Cut(stub, spillMarker)
	if !ok {
		return ""
	}
	path, _, _ := strings.Cut(rest, "\n")
	return strings.TrimSuffix(path, "]")
}

// writeSpill saves content to path unless it's already there. Failing only
// costs the model a way back to the output, so errors are ignored.
func writeSpill(path, content string) {
	if path == "" {
		return
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		_ = os.WriteFile(path, []byte(content), 0o600)
	}
}

// pruneOnce prunes spill files once per process: the cache directory
// isn't cleared on reboot as the temp directory is.
var pruneOnce sync.Once

// pruneSpills removes spill files in dir older than maxAge.
func pruneSpills(dir string, maxAge time.Duration) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// restoreSpills writes back spill files that went missing, as old ones do
// when pruned. Pruning happens here, first, so a file this session still
// names is written back in the same pass.
func (l *Loop) restoreSpills() {
	pruneOnce.Do(func() { pruneSpills(spillDir(), spillMaxAge) })
	for _, msg := range l.History {
		if msg.Role == model.RoleTool {
			writeSpill(spillFrom(msg.Elided), msg.Content)
		}
	}
}
