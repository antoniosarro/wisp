package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGrepTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("no matches here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte("Foo\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gt GrepTool
	res, err := gt.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"pattern":"Foo","path":%q}`, dir)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(res.Content, "a.go:2:func Foo() {}") {
		t.Errorf("Content = %q, want a match on a.go:2", res.Content)
	}
	if strings.Contains(res.Content, "bin.dat") {
		t.Errorf("Content = %q, want binary file excluded", res.Content)
	}
}

func TestGrepToolGlobFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gt GrepTool
	res, err := gt.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"pattern":"target","path":%q,"glob":"*.go"}`, dir)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Content, "a.go") || strings.Contains(res.Content, "a.txt") {
		t.Errorf("Content = %q, want only a.go matched", res.Content)
	}
}

func TestGrepToolTruncates(t *testing.T) {
	orig := maxToolResults
	maxToolResults = 2
	defer func() { maxToolResults = orig }()

	dir := t.TempDir()
	content := strings.Repeat("hit\n", 5)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var gt GrepTool
	res, err := gt.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"pattern":"hit","path":%q}`, dir)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Content, "truncated at 2") {
		t.Errorf("Content = %q, want a truncation note", res.Content)
	}
}

func TestGrepToolInvalidPattern(t *testing.T) {
	var gt GrepTool
	_, err := gt.Run(context.Background(), json.RawMessage(`{"pattern":"("}`))
	if err == nil {
		t.Fatal("expected an error for an invalid regex")
	}
}

func TestGrepToolSingleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("func Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := GrepTool{}.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"pattern":"Foo","path":%q}`, path)))
	if err != nil || res.Content != path+":1:func Foo() {}" {
		t.Fatalf("Run() = %q, %v", res.Content, err)
	}
}

func TestGrepToolOutputModes(t *testing.T) {
	t.Chdir(t.TempDir())
	long := strings.Repeat("x", 5000) + "needle" + strings.Repeat("y", 5000)
	if err := os.MkdirAll("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("sub/a.txt", []byte("needle\n"+long+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args string) string {
		t.Helper()
		res, err := GrepTool{}.Run(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return res.Content
	}

	got := run(`{"pattern":"needle","path":"sub"}`)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || lines[0] != "sub/a.txt:1:needle" || !strings.Contains(lines[1], "needle") || len(lines[1]) > 400 {
		t.Errorf("matches = %q, want paths from the working directory and the long line cut around the match", got)
	}
	if got := run(`{"pattern":"needle","files_only":true}`); got != "sub/a.txt" {
		t.Errorf("files_only = %q, want the file once", got)
	}
	if got := run(`{"pattern":"absent"}`); !strings.HasPrefix(got, "(no matches") {
		t.Errorf("no match = %q, want an explicit note", got)
	}
}

// A FIFO in the tree is skipped, not opened: opening it would block.
func TestGrepSkipsFIFOs(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	writeFile(t, filepath.Join(dir, "a.txt"), "needle\n")
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := GrepTool{}.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"pattern":"needle","path":%q}`, dir)))
		if err != nil || !strings.Contains(res.Content, "a.txt") {
			t.Errorf("grep past a FIFO = %+v, %v", res, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked on a FIFO")
	}
}

// A search of the project must not return a .env or a key: reading those
// asks first, and grep over a directory doesn't ask.
func TestGrepSkipsCredentialFilesInAWalk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"), "API_KEY=secret\n")
	writeFile(t, filepath.Join(dir, "deploy", "server.pem"), "API_KEY=secret\n")
	writeFile(t, filepath.Join(dir, ".env.example"), "API_KEY=placeholder\n")
	writeFile(t, filepath.Join(dir, "main.go"), "os.Getenv(\"API_KEY\")\n")
	if err := os.Symlink(filepath.Join(dir, ".env"), filepath.Join(dir, "settings")); err != nil {
		t.Fatal(err)
	}

	got := run(t, GrepTool{}, jsonArgs(map[string]any{"pattern": "API_KEY", "path": dir})).Content
	if strings.Contains(got, "secret") {
		t.Errorf("grep returned a credential file's contents:\n%s", got)
	}
	if !strings.Contains(got, "main.go") || !strings.Contains(got, "placeholder") || !strings.Contains(got, "3 credential file(s)") {
		t.Errorf("grep = %q, want main.go and .env.example, and 3 files skipped", got)
	}

	// Named directly, it is searched: the call asked first (RiskyCall).
	args := jsonArgs(map[string]any{"pattern": "API_KEY", "path": filepath.Join(dir, ".env")})
	if !(GrepTool{}).RiskyCall(json.RawMessage(args)) || !strings.Contains(run(t, GrepTool{}, args).Content, "secret") {
		t.Error("grep of a credential file by path didn't ask, or didn't search it")
	}
}
