package builtin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLsListsDirsFirstWithSizes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "zdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if got := run(t, LsTool{}, jsonArgs(map[string]any{"path": dir})).Content; got != "zdir/\na.txt\t2.0 KB\nlink@" {
		t.Errorf("Content = %q", got)
	}
	if got := run(t, LsTool{}, jsonArgs(map[string]any{"path": filepath.Join(dir, "zdir")})).Content; got != "(empty directory)" {
		t.Errorf("empty dir Content = %q", got)
	}
}

func TestLsTruncates(t *testing.T) {
	defer func(n int) { maxToolResults = n }(maxToolResults)
	maxToolResults = 2
	dir := t.TempDir()
	writeTree(t, dir, "a", "b", "c")
	if got := run(t, LsTool{}, jsonArgs(map[string]any{"path": dir})).Content; got != "a\t1 B\nb\t1 B\n... (1 more, truncated at 2)" {
		t.Errorf("Content = %q", got)
	}
}
