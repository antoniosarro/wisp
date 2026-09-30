package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

func TestSplashShowsTraceURLOnlyWhenTracing(t *testing.T) {
	if strings.Contains(renderSplash(Options{BaseURL: "http://x/v1"}, "s1", 100), "Trace") {
		t.Error("splash shows a trace row without --trace")
	}
	if !strings.Contains(renderSplash(Options{BaseURL: "http://x/v1", TraceURL: "http://127.0.0.1:7777"}, "s1", 100), "http://127.0.0.1:7777") {
		t.Error("splash lacks the trace URL with --trace")
	}
}

func TestSplashShowsUpdateOnlyWhenNewer(t *testing.T) {
	if strings.Contains(renderSplash(Options{BaseURL: "http://x/v1"}, "s1", 100), "Update") {
		t.Error("splash shows an update row without a newer release")
	}
	if !strings.Contains(renderSplash(Options{BaseURL: "http://x/v1", Update: "1.3.0"}, "s1", 100), "Version 1.3.0") {
		t.Error("splash lacks the newer version")
	}
}

// The splash shows the endpoint's model name and details and the
// directory's path: none may drive the terminal.
func TestSplashShowsNoEscapes(t *testing.T) {
	evil := "\x1b]52;c;aGk=\x07\x1b[2J"
	opts := Options{
		Model:   model.Info{ID: "model" + evil},
		WorkDir: "/tmp/repo" + evil,
		BaseURL: "http://x/v1" + evil,
	}
	got := renderSplash(opts, "s1", 120)
	if strings.Contains(got, "\x1b]52") || strings.Contains(got, "\x1b[2J") {
		t.Errorf("splash drew an escape sequence: %q", got)
	}
	if plain := stripANSI(got); !strings.Contains(plain, "model") || !strings.Contains(plain, "/tmp/repo␛]52") {
		t.Errorf("splash lost the names:\n%s", plain)
	}
}

// The splash tops the main chat, and an update found after startup shows
// on it at once.
func TestSplashInChat(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.opts.BaseURL = "http://x/v1"
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if view := stripANSI(m.View()); !strings.Contains(view, "Endpoint") || !strings.Contains(view, "test-model") {
		t.Fatalf("no splash in the chat:\n%s", view)
	}
	m.Update(UpdateMsg("9.9.9"))
	if !strings.Contains(stripANSI(m.View()), "Version 9.9.9") {
		t.Error("a found update isn't on the splash")
	}
}
