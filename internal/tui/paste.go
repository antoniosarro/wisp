package tui

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/session"
)

// A long paste, and an image pasted with Ctrl+V, go in the input as a
// label such as [Pasted text #1 +40 lines] or [Image #2], which the
// session store keeps (session.Paste). The chat shows labels; the model
// gets the text and the image.

// A paste over pasteLines lines or pasteBytes bytes is shown as a label.
const (
	pasteLines = 10
	pasteBytes = 1000
)

// maxImageBytes is the largest image Ctrl+V attaches, as the read tool's.
const maxImageBytes = 10 << 20

// clipboardMsg is what Ctrl+V found: an image, or else the clipboard's text.
type clipboardMsg struct {
	image *model.Image
	text  string
	err   error
}

// insertPaste puts a bracketed paste in the input, as a label when long.
func (m *Model) insertPaste(text string) {
	store, ok := m.loop.Store.(*session.Store)
	if !ok || strings.Count(text, "\n") < pasteLines && len(text) <= pasteBytes {
		m.input.InsertString(text)
		return
	}
	p, err := store.SavePaste(m.loop.SessionID, text, nil)
	if err != nil {
		m.notice = "Paste failed: " + err.Error()
		return
	}
	m.pastes = append(m.pastes, p)
	m.input.InsertString(p.Label)
}

// pasteClipboard reads the clipboard off the Update goroutine: an image if
// it holds one, its text otherwise.
func (m *Model) pasteClipboard() tea.Cmd {
	return func() tea.Msg {
		img, err := clipboardImage()
		if err != nil || img != nil {
			return clipboardMsg{image: img, err: err}
		}
		text, err := clipboard.ReadAll()
		return clipboardMsg{text: text, err: err}
	}
}

// handleClipboard inserts what Ctrl+V found.
func (m *Model) handleClipboard(msg clipboardMsg) {
	switch {
	case msg.err != nil:
		m.notice = "Paste failed: " + msg.err.Error()
	case msg.image == nil:
		m.insertPaste(msg.text)
	case m.opts.Model.Vision == model.Unsupported:
		m.notice = "This model can't see images: switch to one with vision (/model) to paste one"
	default:
		store := m.sessionStore()
		if store == nil {
			return
		}
		p, err := store.SavePaste(m.loop.SessionID, "", msg.image)
		if err != nil {
			m.notice = "Paste failed: " + err.Error()
			return
		}
		m.pastes = append(m.pastes, p)
		m.input.InsertString(p.Label)
	}
	m.applyLayout()
}

// deleteLabel deletes a whole paste label when Backspace (msg) would
// delete its closing bracket, or Delete its opening one, reporting whether
// it did. A label edited by hand is text like any other.
func (m *Model) deleteLabel(msg tea.KeyMsg) bool {
	forward := msg.String() == "delete"
	if !forward && msg.String() != "backspace" {
		return false
	}
	line := []rune(strings.Split(m.input.Value(), "\n")[m.input.Line()])
	info := m.input.LineInfo()
	col := info.StartColumn + info.ColumnOffset
	for _, p := range m.pastes {
		n := len([]rune(p.Label))
		start := col - n
		if forward {
			start = col
		}
		if start < 0 || start+n > len(line) || string(line[start:start+n]) != p.Label {
			continue
		}
		for range n { // key by key, so the cursor stays where it is
			m.input, _ = m.input.Update(msg)
		}
		return true
	}
	return false
}

// loadPastes reads the session's pastes, for its labels to work.
func (m *Model) loadPastes() {
	m.pastes = nil
	if store, ok := m.loop.Store.(*session.Store); ok {
		m.pastes, _ = store.Pastes(m.loop.SessionID) // without them, prompts show in full
	}
}

// shownPrompt is a prompt as the chat shows it: with its pastes' labels.
func (m *Model) shownPrompt(sent string) string {
	return session.Collapse(strings.TrimSuffix(sent, core.ImageDroppedNote), m.pastes)
}

// clipboardImage returns the image on the clipboard, or nil when it holds
// none or there is no tool to ask: wl-paste on Wayland, xclip on X11.
func clipboardImage() (*model.Image, error) {
	list, get := []string{"wl-paste", "--list-types"}, []string{"wl-paste", "--no-newline", "--type"}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		list, get = []string{"xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"}, []string{"xclip", "-selection", "clipboard", "-o", "-t"}
	}
	if _, err := exec.LookPath(list[0]); err != nil {
		return nil, nil
	}
	types, err := exec.Command(list[0], list[1:]...).Output()
	if err != nil {
		return nil, nil // an empty clipboard is an error to both
	}
	offered := strings.Fields(string(types))
	for _, mime := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		if !slices.Contains(offered, mime) {
			continue
		}
		data, err := exec.Command(get[0], append(get[1:], mime)...).Output()
		if err != nil {
			return nil, fmt.Errorf("reading the clipboard's image: %w", err)
		}
		if len(data) > maxImageBytes {
			return nil, fmt.Errorf("the clipboard's image is %d MB, over the %d MB limit", len(data)>>20, maxImageBytes>>20)
		}
		return &model.Image{MIME: mime, Data: data}, nil
	}
	return nil, nil
}
