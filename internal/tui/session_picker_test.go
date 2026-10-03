package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/testutil"
)

// openSessions opens the session picker on a store holding prompts, the
// newest chosen.
func openSessions(t *testing.T, prompts ...string) *Model {
	t.Helper()
	store, ids := sessionStoreWith(t, prompts...)
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Store, m.loop.SessionID = store, ids[0] // the oldest is in use
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.pickSession()
	if m.modal == nil {
		t.Fatal("no picker")
	}
	m.modal.index = 0
	return m
}

func press(m *Model, k tea.KeyType) { m.Update(tea.KeyMsg{Type: k}) }

func titles(m *Model) []string {
	var out []string
	for _, it := range m.modal.items {
		out = append(out, it.title)
	}
	return out
}

func TestSessionPickerStarsPinToTop(t *testing.T) {
	m := openSessions(t, "fix the parser", "add a cache", "write docs")
	m.modal.index = 2 // "fix the parser", the oldest
	press(m, tea.KeyCtrlS)
	if got := strings.Join(titles(m), ","); got != "fix the parser,write docs,add a cache" {
		t.Fatalf("after starring: %s", got)
	}
	if !m.modal.items[0].starred || m.modal.index != 0 {
		t.Errorf("the starred session isn't marked and chosen: %+v, index %d", m.modal.items[0], m.modal.index)
	}
	if !strings.Contains(stripANSI(m.View()), "★ fix the parser") {
		t.Errorf("no star shown:\n%s", stripANSI(m.View()))
	}
	press(m, tea.KeyCtrlS)
	if got := strings.Join(titles(m), ","); got != "write docs,add a cache,fix the parser" {
		t.Errorf("after unstarring: %s", got)
	}
}

func TestSessionPickerRenames(t *testing.T) {
	m := openSessions(t, "fix the parser", "add a cache")
	press(m, tea.KeyCtrlR)
	if m.modal.ask == nil || m.modal.ask.text != "add a cache" {
		t.Fatalf("rename didn't start from the current name: %+v", m.modal.ask)
	}
	press(m, tea.KeyCtrlU)
	typeRunes(m, "cache work")
	press(m, tea.KeyEnter)
	if m.modal.ask != nil || titles(m)[0] != "cache work" {
		t.Errorf("after renaming: %v", titles(m))
	}
	press(m, tea.KeyCtrlR)
	typeRunes(m, " ignored")
	press(m, tea.KeyEsc)
	if m.modal == nil || titles(m)[0] != "cache work" {
		t.Error("Esc should cancel the rename and keep the picker open")
	}
}

func TestSessionPickerDeletesAfterConfirming(t *testing.T) {
	m := openSessions(t, "fix the parser", "add a cache")
	press(m, tea.KeyCtrlD)
	if m.modal.ask == nil || !strings.Contains(stripANSI(m.View()), `Delete "add a cache"?`) {
		t.Fatalf("no confirmation:\n%s", stripANSI(m.View()))
	}
	typeRunes(m, "n")
	if len(m.modal.items) != 2 || m.modal.ask != nil {
		t.Fatal("a key other than y should cancel")
	}
	press(m, tea.KeyCtrlD)
	typeRunes(m, "y")
	if got := titles(m); len(got) != 1 || got[0] != "fix the parser" {
		t.Fatalf("after deleting: %v", got)
	}
	if exists, _ := m.sessionStore().SessionExists(m.modal.items[0].value); !exists {
		t.Error("the other session went too")
	}
	// The one left is the session in use: not deleted.
	press(m, tea.KeyCtrlD)
	if m.modal.ask != nil || !strings.Contains(m.modal.status, "session you're in") {
		t.Errorf("deleting the current session: ask %+v, status %q", m.modal.ask, m.modal.status)
	}
}
