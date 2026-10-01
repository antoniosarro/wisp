package permission

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTerminalPrompterAnswers(t *testing.T) {
	for in, want := range map[string]Decision{
		"y\n": Allow, "YES\n": Allow, "a\n": AllowAlways, "always\r\n": AllowAlways,
		"n\n": Deny, "no\n": Deny, "\n": Deny, "": Deny, "maybe\n": Deny,
		"y": Allow, // EOF ends the line
	} {
		if got := (TerminalPrompter{In: strings.NewReader(in), Out: io.Discard}).Prompt("bash", nil); got != want {
			t.Errorf("answer %q = %v, want %v", in, got, want)
		}
	}
}

func TestTerminalPrompterPreservesFollowingAnswer(t *testing.T) {
	p := TerminalPrompter{In: strings.NewReader("y\nn\n"), Out: io.Discard}
	if p.Prompt("first", nil) != Allow {
		t.Fatal("first approval lost")
	}
	if p.Prompt("second", nil) != Deny {
		t.Fatal("second denial lost")
	}
}

// The prompt names the tool and who asks, shows control characters instead
// of letting the terminal act on them, and says what "always" covers.
func TestTerminalPrompterShowsTheCall(t *testing.T) {
	var out strings.Builder
	p := TerminalPrompter{In: strings.NewReader("n\n"), Out: &out}
	p.PromptContext(WithOrigin(context.Background(), "explorer"), "bash", json.RawMessage(`{"command":"git status\u001b[2K"}`))
	got := out.String()
	for _, want := range []string{"sub-agent explorer wants to run: bash(", "always allow this command"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("prompt %q passes an escape character to the terminal", got)
	}
}

func TestTerminalPrompterContextCancellation(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = r.Close(); _ = w.Close() }()
	p := TerminalPrompter{In: r, Out: io.Discard}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan Decision, 1)
	go func() { result <- p.PromptContext(ctx, "bash", nil) }()
	cancel()
	select {
	case d := <-result:
		if d != Deny {
			t.Fatal("canceled request allowed")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked on stdin")
	}
}

// fetch labels a URL that doesn't parse with the raw string, control
// characters and all: the prompt must still not pass them through.
func TestTerminalPrompterSanitizesTheRuleLabel(t *testing.T) {
	var out strings.Builder
	args, _ := json.Marshal(map[string]string{"url": "x\x1b[1A\x1b[2Kallowed"})
	TerminalPrompter{In: strings.NewReader("n\n"), Out: &out}.Prompt("fetch", args)
	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("prompt %q passes an escape character to the terminal", out.String())
	}
}

// The file tools follow symlinks, so a write to a link in the project
// lands in its target, such as ~/.bashrc: the prompt says so, rather than
// naming only the link.
func TestTerminalPrompterNamesSymlinkTarget(t *testing.T) {
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
	prompt := func(name string, args map[string]any) string {
		t.Helper()
		raw, _ := json.Marshal(args)
		var out strings.Builder
		TerminalPrompter{In: strings.NewReader("n\n"), Out: &out}.Prompt(name, raw)
		return out.String()
	}

	for _, name := range []string{"write", "edit", "multi_edit"} {
		if got := prompt(name, map[string]any{"path": link}); !strings.Contains(got, "the path is a symlink: this writes "+target+"\n") {
			t.Errorf("%s through a link: prompt %q doesn't name the target", name, got)
		}
	}
	// A plain file, a new one, and a tool that doesn't write get no such line.
	for name, args := range map[string]map[string]any{
		"write": {"path": target},
		"edit":  {"path": filepath.Join(dir, "new.txt")},
		"read":  {"path": link},
	} {
		if got := prompt(name, args); strings.Contains(got, "symlink") {
			t.Errorf("%s %v: prompt %q mentions a symlink", name, args, got)
		}
	}
}
