package permission

import (
	"context"
	"encoding/json"
	"io"
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
