package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/session"
	"github.com/antoniosarro/wisp/internal/testutil"
)

// pasteModel is a test model running a new session of a real store.
func pasteModel(t *testing.T) (*Model, chan tea.Msg, *session.Store) {
	t.Helper()
	store, _ := sessionStoreWith(t)
	m, ch := newTestModel(t, &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "ok"}, {Kind: model.EventDone}}}})
	id, err := store.CreateSession("m")
	if err != nil {
		t.Fatal(err)
	}
	m.loop.Store, m.loop.SessionID = store, id
	return m, ch, store
}

// A long paste shows as its label, in the input and the chat, also after
// a resume; the model gets the text.
func TestLongPasteShowsAsLabel(t *testing.T) {
	m, ch, _ := pasteModel(t)
	long := strings.Repeat("a line of the log\n", 20)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("look at ")})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(long), Paste: true})
	if got := m.input.Value(); got != "look at [Pasted text #1 +21 lines]" {
		t.Fatalf("input = %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pump(t, m, ch)
	if got := m.loop.History[0].Content; got != "look at "+long {
		t.Errorf("sent %q, want the pasted text in place of its label", got)
	}
	for _, when := range []string{"sent", "resumed"} {
		if when == "resumed" {
			m.resumeSession(m.loop.SessionID)
		}
		if got := transcriptText(m); !strings.Contains(got, "look at [Pasted text #1 +21 lines]") || strings.Contains(got, "a line of the log") {
			t.Errorf("%s: the chat should show the label:\n%s", when, got)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "look at [Pasted text #1 +21 lines]" {
		t.Errorf("recalled %q, want the label", got)
	}
}

// A short paste goes in as it is.
func TestShortPasteGoesInAsIs(t *testing.T) {
	m, _, _ := pasteModel(t)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("two\nlines"), Paste: true})
	if got := m.input.Value(); got != "two\nlines" {
		t.Errorf("input = %q", got)
	}
}

// An image pasted with Ctrl+V is saved with the session and attached; the
// chat shows its label, the model also gets the file's path.
func TestPastedImageIsAttached(t *testing.T) {
	m, ch, _ := pasteModel(t)
	img := model.Image{MIME: "image/png", Data: []byte("\x89PNG fake")}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("what is ")})
	m.Update(clipboardMsg{image: &img})
	if got := m.input.Value(); got != "what is [Image #1]" {
		t.Fatalf("input = %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pump(t, m, ch)
	sent := m.loop.History[0]
	if len(sent.Images) != 1 || sent.Images[0].MIME != "image/png" || !strings.HasPrefix(sent.Content, "what is [Image #1: ") {
		t.Fatalf("sent %q with %+v", sent.Content, sent.Images)
	}
	path := strings.TrimSuffix(strings.TrimPrefix(sent.Content, "what is [Image #1: "), "]")
	if filepath.Base(filepath.Dir(path)) != m.loop.SessionID {
		t.Errorf("image saved at %s, want a folder of the session's", path)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(img.Data) {
		t.Errorf("saved image: %q, %v", data, err)
	}
	if got := transcriptText(m); !strings.Contains(got, "what is [Image #1]") || strings.Contains(got, path) {
		t.Errorf("the chat should show the label, not the path:\n%s", got)
	}
}

// A model without vision gets no image, and the user is told why.
func TestPastedImageNeedsVision(t *testing.T) {
	m, _, _ := pasteModel(t)
	m.opts.Model.Vision = model.Unsupported
	m.Update(clipboardMsg{image: &model.Image{MIME: "image/png", Data: []byte("x")}})
	if m.input.Value() != "" || len(m.pastes) != 0 || !strings.Contains(m.notice, "can't see images") {
		t.Errorf("input %q, %d pastes, notice %q", m.input.Value(), len(m.pastes), m.notice)
	}
}

// Backspace after a label's ] deletes the whole label, and Delete before
// its [ does too; elsewhere they delete one character.
func TestDeletingALabelDeletesItWhole(t *testing.T) {
	m, _, _ := pasteModel(t)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("see ")})
	m.Update(clipboardMsg{image: &model.Image{MIME: "image/png", Data: []byte("x")}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ok")})
	for range " ok" {
		m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.input.Value(); got != "see  ok" {
		t.Fatalf("after Backspace: %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.input.Value(); got != "see ok" {
		t.Errorf("a plain Backspace: %q", got)
	}

	m.input.SetValue("")
	m.Update(clipboardMsg{image: &model.Image{MIME: "image/png", Data: []byte("y")}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	m.input.CursorStart()
	m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if got := m.input.Value(); got != "!" {
		t.Errorf("after Delete: %q", got)
	}
}
