package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditTool(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	var et EditTool
	_, err := et.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q,"old_string":"world","new_string":"there"}`, p)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, _ := os.ReadFile(p)
	if string(got) != "hello there" {
		t.Errorf("file content = %q, want %q", got, "hello there")
	}
}

func TestEditToolAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("foo foo foo"), 0o644); err != nil {
		t.Fatal(err)
	}

	var et EditTool
	_, err := et.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q,"old_string":"foo","new_string":"bar"}`, p)))
	if err == nil {
		t.Fatal("expected an error for a non-unique old_string without replace_all")
	}

	got, _ := os.ReadFile(p)
	if string(got) != "foo foo foo" {
		t.Errorf("file should be unchanged on error, got %q", got)
	}
}

func TestEditToolReplaceAll(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("foo foo foo"), 0o644); err != nil {
		t.Fatal(err)
	}

	var et EditTool
	res, err := et.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q,"old_string":"foo","new_string":"bar","replace_all":true}`, p)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Content != "replaced 3 occurrence(s) in "+p {
		t.Errorf("Content = %q", res.Content)
	}

	got, _ := os.ReadFile(p)
	if string(got) != "bar bar bar" {
		t.Errorf("file content = %q, want %q", got, "bar bar bar")
	}
}

func TestEditToolNotFound(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	var et EditTool
	_, err := et.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q,"old_string":"missing","new_string":"x"}`, p)))
	if err == nil {
		t.Fatal("expected an error when old_string isn't found")
	}
}

func TestEditToolIdenticalStrings(t *testing.T) {
	var et EditTool
	_, err := et.Run(context.Background(), json.RawMessage(`{"path":"f.txt","old_string":"same","new_string":"same"}`))
	if err == nil {
		t.Fatal("expected an error when old_string equals new_string")
	}
}

func TestEditToolPreservesFileMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.sh")
	if err := os.WriteFile(p, []byte("echo hi"), 0o755); err != nil {
		t.Fatal(err)
	}

	var et EditTool
	_, err := et.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q,"old_string":"hi","new_string":"bye"}`, p)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755 preserved", info.Mode().Perm())
	}
}

func TestEditToolMismatchHelp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.go")
	if err := os.WriteFile(p, []byte("func main() {\r\n\tprintln(\"hi\")\r\n}\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit := func(old, new string) error {
		args, _ := json.Marshal(map[string]string{"path": p, "old_string": old, "new_string": new})
		_, err := EditTool{}.Run(context.Background(), args)
		return err
	}

	if err := edit("\tprintln(\"hi\")\n}", "\tprintln(\"bye\")\n}"); err != nil {
		t.Fatalf("LF edit on a CRLF file: %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "func main() {\r\n\tprintln(\"bye\")\r\n}\r\n" {
		t.Errorf("file = %q, want CRLF kept", got)
	}
	for old, hint := range map[string]string{
		"     2\tprintln(\"bye\")": "line-number prefixes",
		"func  main()  {":          "whitespace",
		"func main() {\nother":     "at line 1",
	} {
		if err := edit(old, "x"); err == nil || !strings.Contains(err.Error(), hint) {
			t.Errorf("edit(%q) error = %v, want a hint about %s", old, err, hint)
		}
	}
}

// A missing or null new_string is an error, not a deletion: deleting text
// takes an explicit "".
func TestEditRequiresNewString(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	writeFile(t, path, "keep me")
	for _, args := range []map[string]any{
		{"path": path, "old_string": "keep"},
		{"path": path, "old_string": "keep", "new_string": nil},
	} {
		if runErr(EditTool{}, jsonArgs(args)) == nil {
			t.Errorf("%v accepted", args)
		}
	}
	if data, _ := os.ReadFile(path); string(data) != "keep me" {
		t.Errorf("file changed to %q", data)
	}
}

func TestEditMixedLineEndings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mixed.txt")
	writeFile(t, p, "crlf one\r\ncrlf two\r\nlf one\nlf two\n")
	for _, old := range []string{"lf one\nlf two", "crlf one\ncrlf two"} {
		if err := runErr(EditTool{}, jsonArgs(map[string]any{"path": p, "old_string": old, "new_string": strings.ToUpper(old)})); err != nil {
			t.Errorf("edit %q: %v", old, err)
		}
	}
	if got, _ := os.ReadFile(p); string(got) != "CRLF ONE\r\nCRLF TWO\r\nLF ONE\nLF TWO\n" {
		t.Errorf("file = %q", got)
	}
}

func TestMultiEditIsAllOrNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.go")
	writeFile(t, path, "a := 1\nb := 2\n")
	// Each edit sees the one before: the second matches the first's result.
	ok := fmt.Sprintf(`{"path":%q,"edits":[{"old_string":"a := 1","new_string":"a := 10"},{"old_string":"a := 10\nb","new_string":"a := 10\nc"}]}`, path)
	if res := run(t, MultiEditTool{}, ok); !strings.Contains(res.Content, "replaced 2") {
		t.Fatalf("Content = %q", res.Content)
	}
	if got, _ := os.ReadFile(path); string(got) != "a := 10\nc := 2\n" {
		t.Fatalf("file = %q", got)
	}

	bad := fmt.Sprintf(`{"path":%q,"edits":[{"old_string":"c := 2","new_string":"c := 3"},{"old_string":"missing","new_string":"x"}]}`, path)
	if err := runErr(MultiEditTool{}, bad); err == nil || !strings.Contains(err.Error(), "edit 2") {
		t.Fatalf("err = %v, want edit 2 to fail", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "a := 10\nc := 2\n" {
		t.Fatalf("failed multi_edit changed the file: %q", got)
	}
}
