package tui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/session"
	"github.com/antoniosarro/wisp/internal/span"
	"github.com/antoniosarro/wisp/internal/testutil"
)

// typeRunes types s key by key, without sending it.
func typeRunes(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// transcriptText is the transcript as rendered, without styling.
func transcriptText(m *Model) string { return stripANSI(m.render()) }

func TestCommandPopup(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	typeRunes(m, "/s")
	got := stripANSI(m.View())
	if !strings.Contains(got, "› /sessions") || !strings.Contains(got, "/resume [session]") || strings.Contains(got, "/model") {
		t.Fatalf("popup for /s:\n%s", got)
	}
	if m.popupRows != 4 || m.viewport.Height != 30-2-4-3-1-1 { // /sessions, /session-rename, /resume, /agents; less the mascot's row and the statusline
		t.Errorf("popup rows %d, viewport height %d: the chat box should give up the popup's rows", m.popupRows, m.viewport.Height)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v := m.input.Value(); v != "/resume " {
		t.Errorf("tab completed %q, want %q", v, "/resume ")
	}
	if m.suggestions() != nil || m.popupRows != 0 {
		t.Error("popup still open once an argument can be typed")
	}

	m.input.SetValue("")
	typeRunes(m, "/he")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.suggestions() != nil {
		t.Error("esc didn't close the popup")
	}
	typeRunes(m, "l")
	if len(m.suggestions()) != 1 {
		t.Error("popup didn't reopen after typing on")
	}
}

func TestRecalledCommandKeepsHistoryKeys(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.recordHistory("first")
	m.recordHistory("/help")
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.suggestions() != nil {
		t.Fatal("recalled command opened the popup")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if v := m.input.Value(); v != "first" {
		t.Errorf("second up gave %q, want history to go on", v)
	}
}

func sessionStoreWith(t *testing.T, prompts ...string) (*session.Store, []string) {
	t.Helper()
	store, err := session.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var ids []string
	for _, p := range prompts {
		id, err := store.CreateSession("m")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendMessage(id, model.Message{Role: model.RoleUser, Content: p}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return store, ids
}

func TestSessionPickerFiltersAndResumes(t *testing.T) {
	store, ids := sessionStoreWith(t, "fix the parser", "add a cache", "write docs")
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Store = store
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.input.SetValue("/resume")
	m.submit()
	if m.modal == nil {
		t.Fatal("/resume without an argument didn't open the picker")
	}
	typeRunes(m, "cache")
	got := stripANSI(m.View())
	if !strings.Contains(got, "1 of 3") || !strings.Contains(got, "› add a cache") || strings.Contains(got, "fix the parser") {
		t.Fatalf("filtered picker:\n%s", got)
	}
	for range "cache" {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if len(m.modal.visible()) != 3 {
		t.Error("backspace didn't widen the filter")
	}
	typeRunes(m, "a cache")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.modal != nil || m.loop.SessionID != ids[1] {
		t.Errorf("picked session %s, want %s", m.loop.SessionID, ids[1])
	}

	m.input.SetValue("/sessions")
	m.submit()
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.modal != nil || m.loop.SessionID != ids[1] {
		t.Error("esc didn't close the picker without switching")
	}
}

func TestArgumentSuggestions(t *testing.T) {
	store, ids := sessionStoreWith(t, "fix the parser", "add a cache")
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Store = store
	m.knownModels = []model.Info{{ID: "qwen3-coder"}, {ID: "glm-5.3-flash"}, {ID: "gemma3"}}
	m.opts.Model = model.Info{ID: "gemma3"}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	typeRunes(m, "/model g")
	got := stripANSI(m.View())
	if !strings.Contains(got, "› glm-5.3-flash") || !strings.Contains(got, "gemma3 current") || strings.Contains(got, "qwen3-coder") {
		t.Fatalf("model suggestions:\n%s", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v := m.input.Value(); v != "/model glm-5.3-flash" {
		t.Errorf("tab gave %q", v)
	}

	m.input.SetValue("")
	typeRunes(m, "/resume cache")
	list := m.suggestions()
	if len(list) != 1 || list[0].fill != "/resume "+ids[1] || !list[0].run {
		t.Fatalf("resume suggestions = %+v", list)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.loop.SessionID != ids[1] {
		t.Errorf("enter resumed %s, want %s", m.loop.SessionID, ids[1])
	}
}

func TestClearStartsANewSession(t *testing.T) {
	store, ids := sessionStoreWith(t, "old work")
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Store = store
	m.opts.Model = model.Info{ID: "m"}
	m.resumeSession(ids[0])
	m.input.SetValue("/clear")
	m.submit()
	if m.loop.SessionID == ids[0] || len(m.loop.History) != 0 || m.loop.Store != store {
		t.Fatalf("after /clear: session %s, %d messages", m.loop.SessionID, len(m.loop.History))
	}
	if got := transcriptText(m); strings.Contains(got, "old work") || !strings.Contains(got, "New session") {
		t.Errorf("transcript after /clear:\n%s", got)
	}
	if sessions, _ := store.ListSessions(); len(sessions) != 1 {
		t.Errorf("%d sessions saved, want only the old one until the new one has a message", len(sessions))
	}
	if err := m.loop.Store.AppendMessage(m.loop.SessionID, model.Message{Role: model.RoleUser, Content: "new work"}); err != nil {
		t.Fatal(err)
	}
	if sessions, _ := store.ListSessions(); len(sessions) != 2 {
		t.Errorf("%d sessions saved, want the old one kept and the new one", len(sessions))
	}
}

func TestSessionRename(t *testing.T) {
	store, ids := sessionStoreWith(t, "old work")
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Store = store
	m.resumeSession(ids[0])
	for _, c := range []struct{ input, want string }{
		{`/session-rename "parser  rewrite"`, "parser rewrite"},
		{`/session-rename it's fine`, "it's fine"}, // last: the picker shows it
	} {
		m.input.SetValue(c.input)
		m.submit()
		if title, _ := store.SessionTitle(ids[0]); title != c.want {
			t.Errorf("%s: title %q, want %q", c.input, title, c.want)
		}
	}
	if items := m.sessionItems(); len(items) != 1 || items[0].title != "it's fine" {
		t.Errorf("picker items = %+v, want the title shown", items)
	}
	m.input.SetValue("/session-rename")
	m.submit()
	if title, _ := store.SessionTitle(ids[0]); title != "it's fine" || !strings.Contains(transcriptText(m), "Give the session a name") {
		t.Errorf("an empty name changed the title to %q, or gave no usage", title)
	}
}

func TestSwitchedSessionRecordsItsOwnSpans(t *testing.T) {
	store, ids := sessionStoreWith(t, "first session", "second session")
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "hi"}, {Kind: model.EventDone}}}}
	m, _ := newTestModel(t, p)
	m.loop.Store, m.loop.SessionID = store, ids[0]
	m.loop.Spans = span.NewRecorder(store, ids[0])
	caller := m.loop // what the frontend's caller holds, and closes at exit

	m.resumeSession(ids[1])
	if _, err := m.loop.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	caller.Spans.Close() // at exit: writes out the current recorder

	spansOf := func(id string) []span.Record {
		recs, err := store.Spans(id)
		if err != nil {
			t.Fatal(err)
		}
		return recs
	}
	if recs := spansOf(ids[1]); !slices.ContainsFunc(recs, func(r span.Record) bool { return r.Kind == span.KindTurn }) {
		t.Errorf("resumed session has %d spans and no turn", len(recs))
	}
	if recs := spansOf(ids[0]); len(recs) != 0 {
		t.Errorf("the turn was recorded under the old session too: %d spans", len(recs))
	}
}

func TestCommandPopupEnterRuns(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	typeRunes(m, "/hel")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.input.Value() != "" || !m.helpOpen || !strings.Contains(stripANSI(m.View()), "esc to close") {
		t.Fatalf("enter didn't run /help: input %q", m.input.Value())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.helpOpen {
		t.Error("esc didn't close the help")
	}
}

func TestUnknownCommand(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.input.SetValue("/frobnicate now")
	m.submit()
	if m.inTurn || !strings.Contains(transcriptText(m), "unknown command: /frobnicate now") {
		t.Errorf("transcript:\n%s", transcriptText(m))
	}
}

// Model names come from the endpoint, session previews from the store
// (earlier prompts, pasted text included): none may drive the terminal.
func TestPickerAndPopupShowNoEscapes(t *testing.T) {
	store, _ := sessionStoreWith(t, "fix \x1b]0;pwned\x07the parser")
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Store = store
	m.knownModels = []model.Info{{ID: "evil\x1b[2Jmodel"}}

	m.showModels(m.knownModels, "")
	typeRunes(m, "e")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("\x1b[31m")})
	if v := m.View(); strings.Contains(v, "\x1b[2J") || strings.Contains(v, "\x1b[31m") || !strings.Contains(stripANSI(v), "evilmodel") {
		t.Errorf("model picker: %q", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	m.input.SetValue("/sessions")
	m.submit()
	if v := m.View(); strings.Contains(v, "\x1b]0") || !strings.Contains(stripANSI(v), "fix the parser") {
		t.Errorf("session picker: %q", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	for _, input := range []string{"/model e", "/resume f"} {
		m.input.SetValue("")
		typeRunes(m, input)
		if v := m.View(); strings.Contains(v, "\x1b[2J") || strings.Contains(v, "\x1b]0") || len(m.suggestions()) != 1 {
			t.Errorf("popup for %q: %q", input, v)
		}
	}
}

// The frontend's caller, and the sub-agent tool reading the main model's
// window, hold the loop they were given: a session switch must show there.
func TestSessionSwitchKeepsTheCallersLoop(t *testing.T) {
	store, ids := sessionStoreWith(t, "old work")
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Store = store
	caller := m.loop

	m.resumeSession(ids[0])
	if caller.SessionID != ids[0] || len(caller.History) != 1 {
		t.Fatalf("after /resume the caller's loop runs %q with %d messages", caller.SessionID, len(caller.History))
	}
	m.clearSession()
	if caller.SessionID == ids[0] || caller.SessionID != m.loop.SessionID || len(caller.History) != 0 {
		t.Errorf("after /clear the caller's loop runs %q, the UI's %q", caller.SessionID, m.loop.SessionID)
	}
}

func TestFindSession(t *testing.T) {
	store, ids := sessionStoreWith(t, "a", "b")
	for arg, want := range map[string]string{
		"1":        ids[1], // newest first, as /sessions lists them
		"2":        ids[0],
		ids[0][:6]: ids[0],
		"zzzzzz":   "zzzzzz", // unknown: Resume says so
	} {
		if got, err := findSession(store, arg); err != nil || got != want {
			t.Errorf("findSession(%q) = %q, %v; want %q", arg, got, err, want)
		}
	}
}

// The help fits its width, lists every section and command, and scrolls
// within its modal when the screen is short.
func TestHelpLayout(t *testing.T) {
	for _, w := range []int{30, 60, 100} {
		text := stripANSI(renderHelp(w))
		for _, l := range strings.Split(text, "\n") {
			if ansi.StringWidth(l) > w {
				t.Errorf("width %d: line overflows: %q", w, l)
			}
		}
		for _, want := range []string{"Commands", "Input", "Transcript", "Approvals", "Notes", "/compact [focus]", "always allow"} {
			if !strings.Contains(text, want) {
				t.Errorf("width %d: help lacks %q", w, want)
			}
		}
	}
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.showHelp()
	view := stripANSI(m.View())
	if !strings.Contains(view, "Help") || !strings.Contains(view, "0%") {
		t.Fatalf("short screen: no scrollable help modal:\n%s", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.help.YOffset != 1 {
		t.Errorf("down scrolled the help to %d, want 1", m.help.YOffset)
	}
}
