package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTool(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")

	var wt WriteTool
	_, err := wt.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"hello"}`, p)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("file content = %q, want %q", got, "hello")
	}
}

func TestWriteToolCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a", "b", "f.txt")

	var wt WriteTool
	_, err := wt.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"x"}`, p)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestWriteToolOverwrites(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	var wt WriteTool
	_, err := wt.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"new"}`, p)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, _ := os.ReadFile(p)
	if string(got) != "new" {
		t.Errorf("file content = %q, want %q", got, "new")
	}
}

func TestWriteToolMissingPath(t *testing.T) {
	var wt WriteTool
	_, err := wt.Run(context.Background(), json.RawMessage(`{"content":"x"}`))
	if err == nil {
		t.Fatal("expected an error when path is omitted")
	}
}

// A missing or null content is an error, not an empty file: emptying a
// file takes an explicit "".
func TestWriteRequiresContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	writeFile(t, path, "keep me")
	for _, args := range []map[string]any{{"path": path}, {"path": path, "content": nil}} {
		if runErr(WriteTool{}, jsonArgs(args)) == nil {
			t.Errorf("%v accepted", args)
		}
	}
	if data, _ := os.ReadFile(path); string(data) != "keep me" {
		t.Errorf("file changed to %q", data)
	}
	if err := runErr(WriteTool{}, jsonArgs(map[string]any{"path": path, "content": ""})); err != nil {
		t.Errorf("explicit empty content rejected: %v", err)
	}
}

func TestWriteIsAtomicAndKeepsModeAndLinks(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(script, []byte("echo a\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.sh")
	if err := os.Symlink(script, link); err != nil {
		t.Fatal(err)
	}
	run(t, WriteTool{}, jsonArgs(map[string]any{"path": link, "content": "echo b\n"}))
	info, _ := os.Lstat(link)
	target, _ := os.Stat(script)
	data, _ := os.ReadFile(script)
	if info.Mode()&os.ModeSymlink == 0 || target.Mode().Perm() != 0o755 || string(data) != "echo b\n" {
		t.Fatalf("link kept %v, target mode %v, content %q", info.Mode()&os.ModeSymlink != 0, target.Mode().Perm(), data)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".*wisp-*")); len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}
