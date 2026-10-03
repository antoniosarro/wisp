package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/session"
	"github.com/antoniosarro/wisp/internal/span"
	tea "github.com/charmbracelet/bubbletea"
)

// sessionStore is the loop's session store, saying so if it has none.
func (m *Model) sessionStore() *session.Store {
	store, ok := m.loop.Store.(*session.Store)
	if !ok {
		m.notify("Session storage is unavailable.")
	}
	return store
}

// sessionItems lists recent sessions for the picker and the popup.
func (m *Model) sessionItems() []pickerItem {
	store, ok := m.loop.Store.(*session.Store)
	if !ok {
		return nil
	}
	sessions, err := store.ListSessions()
	if err != nil {
		return nil
	}
	items := make([]pickerItem, len(sessions))
	for i, s := range sessions {
		items[i] = pickerItem{
			value:   s.ID,
			title:   s.OneLinePreview(),
			detail:  s.CreatedAt.Format("2006-01-02 15:04") + " · " + s.Model + " · " + s.ID[:min(8, len(s.ID))],
			current: s.ID == m.loop.SessionID,
		}
	}
	return items
}

// pickSession opens the session picker; choosing one resumes it.
func (m *Model) pickSession() {
	if m.sessionStore() == nil {
		return
	}
	items := m.sessionItems()
	if len(items) == 0 {
		m.notify("No saved sessions in this directory.")
		return
	}
	m.openPicker(&picker{title: "Resume a session", items: items, pick: func(id string) tea.Cmd {
		m.resumeSession(id)
		return nil
	}})
}

// findSession resolves a /sessions list number, a unique id prefix, or a
// full id.
func findSession(store *session.Store, arg string) (string, error) {
	sessions, err := store.ListSessions()
	if err != nil {
		return "", err
	}
	if i, err := strconv.Atoi(arg); err == nil && i > 0 && i <= len(sessions) && len(arg) < 8 {
		return sessions[i-1].ID, nil
	}
	var found []string
	for _, s := range sessions {
		if strings.HasPrefix(s.ID, arg) {
			found = append(found, s.ID)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return arg, nil // maybe older than the list; Resume reports unknown ids
	}
	return "", fmt.Errorf("%q matches %d sessions; type more of the id", arg, len(found))
}

// resumeSession continues the session id names (see findSession).
func (m *Model) resumeSession(id string) {
	if m.inTurn {
		m.notify("Cancel or finish the current turn before switching sessions.")
		return
	}
	store := m.sessionStore()
	if store == nil {
		return
	}
	id, err := findSession(store, id)
	if err != nil {
		m.notify(err.Error())
		return
	}
	history, err := store.Resume(id)
	if err != nil {
		m.notify(err.Error())
		return
	}
	m.switchSession(store, id, history)
}

// clearSession starts a new session in the same store, with the same
// model and tools; the current one stays saved for /resume.
func (m *Model) clearSession() {
	if m.inTurn {
		m.notify("Cancel or finish the current turn before starting a new session.")
		return
	}
	store := m.sessionStore()
	if store == nil {
		return
	}
	saved := len(m.loop.History) > 0 // an empty session is never stored
	id, err := store.CreateSession(m.opts.Model.ID)
	if err != nil {
		m.notify(err.Error())
		return
	}
	m.switchSession(store, id, nil)
	if saved {
		m.notify("New session. The previous one is saved: /resume lists it.")
	} else {
		m.notify("New session.")
	}
}

// renameSession gives the current session the title arg, quotes around it
// optional, which session lists show in place of its first prompt.
func (m *Model) renameSession(arg string) {
	if len(arg) >= 2 && (arg[0] == '"' || arg[0] == '\'') && arg[len(arg)-1] == arg[0] {
		arg = arg[1 : len(arg)-1]
	}
	title := strings.Join(strings.Fields(arg), " ") // one line
	if title == "" {
		m.notify(`Give the session a name: /session-rename "parser rewrite".`)
		return
	}
	store := m.sessionStore()
	if store == nil {
		return
	}
	if err := store.RenameSession(m.loop.SessionID, title); err != nil {
		m.notify(err.Error())
		return
	}
	m.notify(fmt.Sprintf("Session renamed to %q.", title))
}

// switchSession makes the loop run session id, with its history, and
// rebuilds the transcript from it. The loop changes in place: the
// frontend's caller and the sub-agent tool hold it too, to close its
// session and follow its model, and must see the session it runs now.
func (m *Model) switchSession(store *session.Store, id string, history []model.Message) {
	m.loop.StopPresummary() // it may still record under the old session
	spans := m.loop.Spans
	if spans != nil {
		// A recorder writes under one session: finish the old one's
		// spans, and give the new session its own.
		spans.Close()
		spans = span.NewRecorder(store, id)
	}
	*m.loop = *m.loop.WithSession(store, id, history, spans)
	if err := m.loop.LoadCompaction(); err != nil {
		m.notify(err.Error() + "; resuming without the summary")
	}
	m.stats, m.cost = core.StepStats{}, costSummary{}
	m.rebuildTranscript()
}

// rebuildTranscript renders the chat again from the loop's history.
func (m *Model) rebuildTranscript() {
	m.clearSuggestion()
	m.blocks = nil
	m.inputHistory, m.historyIndex, m.draft = nil, 0, ""
	m.todos, m.todoOpen = nil, false
	m.agentRuns, m.agentsOpen = nil, false
	m.runViews, m.viewing = map[int64]*blockList{}, 0
	m.selectedBlock, m.hoverBlock = -1, -1
	m.sel = textSelection{}
	m.notice = ""
	m.replayHistory(m.loop.History)
	m.autoScroll = true
	m.applyLayout()
}

// undoTurn takes back the last turn and the files it changed, and puts its
// prompt back in the input box to edit and send again.
func (m *Model) undoTurn() {
	if m.inTurn {
		m.notify("Cancel or finish the current turn before undoing it.")
		return
	}
	u, err := m.loop.Undo()
	if err != nil {
		if len(u.Files) > 0 {
			err = fmt.Errorf("%w (restored %s first)", err, strings.Join(u.Files, ", "))
		}
		m.notify("Undo failed: " + err.Error())
		return
	}
	m.rebuildTranscript()
	m.input.SetValue(m.shownPrompt(u.Input))
	m.input.CursorEnd()
	msg := "Undid the last turn"
	switch len(u.Files) {
	case 0:
	case 1:
		msg += " and restored " + u.Files[0]
	default:
		msg += fmt.Sprintf(" and restored %d files", len(u.Files))
	}
	msg += "."
	if len(u.Untracked) > 0 {
		msg += " Changes made by " + strings.Join(u.Untracked, ", ") + " can't be undone and stay as they are."
	}
	m.notify(msg)
}
