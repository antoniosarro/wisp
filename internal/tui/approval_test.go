package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// writeDetails is what the approval shows for a write of content to path.
func writeDetails(t *testing.T, path, content string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"path": path, "content": content})
	return ansi.Strip(permissionDetails("write", args, filepath.Dir(path)))
}

// Overwriting a text file shows the lines that change, with context.
func TestWriteApprovalDiffsAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("# Notes\nkeep\nold line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := writeDetails(t, path, "# Notes\nkeep\nnew line\nadded\n")
	for _, want := range []string{"wants to overwrite", "4 lines", "  keep", "- old line", "+ new line", "+ added"} {
		if !strings.Contains(got, want) {
			t.Errorf("details lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "wants to create") {
		t.Errorf("an existing file is described as created:\n%s", got)
	}
}

// Files a diff can't sensibly show fall back to the new content's start.
func TestWriteApprovalFallsBackToContent(t *testing.T) {
	dir := t.TempDir()
	distinct := func(prefix string, n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "%s %d\n", prefix, i)
		}
		return b.String()
	}
	for _, c := range []struct {
		name, old, content, want string
	}{
		{"binary", "\x00\x01", "text\n", "text"},
		{"too big to read", strings.Repeat("x", maxPreviewBytes+1), "text\n", "text"},
		{"too big to diff", distinct("old", 2100), distinct("new", 2100), "too large to diff; the new content starts:"},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "_"))
		if err := os.WriteFile(path, []byte(c.old), 0o644); err != nil {
			t.Fatal(err)
		}
		got := writeDetails(t, path, c.content)
		if !strings.Contains(got, c.want) || strings.Contains(got, "- ") {
			t.Errorf("%s: details are not a content preview:\n%.300s", c.name, got)
		}
	}
}

// A long new file shows its first lines and how many more there are.
func TestWriteApprovalPreviewIsCapped(t *testing.T) {
	content := strings.Repeat("line\n", maxWritePreviewLines+5)
	got := writeDetails(t, filepath.Join(t.TempDir(), "new.txt"), content)
	if !strings.Contains(got, "wants to create") || !strings.Contains(got, "… 5 more lines") {
		t.Errorf("details:\n%s", got)
	}
	if n := strings.Count(got, "line\n"); n > maxWritePreviewLines {
		t.Errorf("preview shows %d lines, want at most %d", n, maxWritePreviewLines)
	}
}

// The file tools follow symlinks: a link in the project can aim a write
// at ~/.bashrc, and the approval must say so, not just name the link.
func TestApprovalShowsWhereASymlinkLeads(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "outside", "bashrc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("export PATH\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "notes.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	edit, _ := json.Marshal(map[string]string{"path": link, "old_string": "a", "new_string": "b"})
	multi, _ := json.Marshal(map[string]any{"path": link, "edits": []map[string]string{{"old_string": "a", "new_string": "b"}}})
	for name, args := range map[string]json.RawMessage{"edit": edit, "multi_edit": multi} {
		if got := ansi.Strip(permissionDetails(name, args, dir)); !strings.Contains(got, "→ "+target) {
			t.Errorf("%s approval hides the link's target:\n%s", name, got)
		}
	}
	if got := writeDetails(t, link, "x\n"); !strings.Contains(got, "wants to overwrite "+link+" → "+target) {
		t.Errorf("write approval hides the link's target:\n%s", got)
	}

	// A plain file shows no arrow, even with a link on the way to it, as
	// when the home directory is one.
	linkedDir := filepath.Join(dir, "linked")
	if err := os.Symlink(filepath.Dir(target), linkedDir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{target, filepath.Join(linkedDir, "bashrc"), filepath.Join(dir, "new.txt")} {
		if got := writeDetails(t, p, "x\n"); strings.Contains(got, "→") {
			t.Errorf("write to %s shows an arrow:\n%s", p, got)
		}
	}
}

// The path is escaped for display, but the file is looked up by the path
// as sent: a file whose name has a control character is still one that
// exists.
func TestWriteApprovalFindsFileWithControlCharacter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "odd\x1bname")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := writeDetails(t, path, "new\n")
	if !strings.Contains(got, "wants to overwrite") || !strings.Contains(got, "- old") || !strings.Contains(got, "odd␛name") {
		t.Errorf("details:\n%s", got)
	}
}

// The working directory comes from the filesystem, and a repository's
// directory can be named anything.
func TestApprovalEscapesWorkDir(t *testing.T) {
	got := permissionDetails("bash", json.RawMessage(`{"command":"ls"}`), "/tmp/repo\x1b]0;x\x07")
	if strings.Contains(got, "\x1b]") || !strings.Contains(ansi.Strip(got), "in /tmp/repo␛]0;x␇") {
		t.Errorf("details = %q, want the directory with its controls shown", got)
	}
}

func TestMultiEditApproval(t *testing.T) {
	details := stripANSI(permissionDetails("multi_edit", json.RawMessage(`{"path":"a.go","edits":[{"old_string":"x","new_string":"y"},{"old_string":"p","new_string":"q"}]}`), ""))
	for _, w := range []string{"2 edits to a.go", "edit 1", "- x", "+ y", "edit 2", "- p", "+ q"} {
		if !strings.Contains(details, w) {
			t.Errorf("multi_edit approval missing %q:\n%s", w, details)
		}
	}
}

// Commands are shown exactly: an escape sequence in one appears as ␛, not
// acted on, so what the box shows is what runs.
func TestBashApprovalShowsControls(t *testing.T) {
	got := permissionDetails("bash", json.RawMessage(`{"command":"echo hi\u001b[2K\rrm -rf ~"}`), "")
	if strings.Contains(got, "\x1b[2K") || !strings.Contains(ansi.Strip(got), "echo hi␛[2K␍rm -rf ~") {
		t.Errorf("details = %q, want the command with its controls shown", got)
	}
}
