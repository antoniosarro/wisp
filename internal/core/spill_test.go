package core

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

// Masked command output is saved in the user's own cache directory, not
// the shared temp directory, and only the user can read it.
func TestSpillsGoToTheUserCache(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	path := spillPath(call("b", "bash", `{"command":"make"}`), "output")
	if want := filepath.Join(cache, "wisp", "masked"); filepath.Dir(path) != want {
		t.Fatalf("spill path %s, want it in %s", path, want)
	}
	if spillPath(call("r", "read", `{"path":"a.go"}`), "output") != "" {
		t.Error("an idempotent call's output was spilled")
	}

	writeSpill(path, "output")
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatalf("spill directory: %v, mode %v; want 0700", err, dir.Mode().Perm())
	}
	if file, err := os.Stat(path); err != nil || file.Mode().Perm() != 0o600 {
		t.Fatalf("spill file: %v; want mode 0600", err)
	}
}

// Old spill files are pruned, and one the session still names comes back
// in the same pass.
func TestRestoreSpillsPrunesAndRestores(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	pruneOnce = sync.Once{} // the test's process may have pruned already
	dir := spillDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * spillMaxAge)
	stale := filepath.Join(dir, "stale.txt")
	named := spillPath(call("b", "bash", `{"command":"make"}`), "full output")
	recent := filepath.Join(dir, "recent.txt")
	for _, p := range []string{stale, named, recent} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{stale, named} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	l := &Loop{History: []model.Message{{Role: model.RoleTool, Content: "full output", Elided: "[bash: masked" + spillMarker + named + "]"}}}
	l.restoreSpills()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("an old spill file nothing names was kept")
	}
	if b, err := os.ReadFile(named); err != nil || string(b) != "full output" {
		t.Errorf("the spill file the session names = %q, %v; want it written back", b, err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("a recent spill file was pruned: %v", err)
	}
	if !strings.HasPrefix(named, cache) {
		t.Errorf("spill path %s outside the cache %s", named, cache)
	}
}
