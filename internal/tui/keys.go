package tui

import (
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/permission"
)

const (
	// approvalGuard is how long a prompt must be visible, and typing must
	// have paused, before y/a/n/t answer it.
	approvalGuard = 400 * time.Millisecond
	// quitWindow is how soon a second Ctrl+C must follow the first to exit.
	quitWindow = 2 * time.Second
)

// handleKey handles a key press: answering approvals, cancelling and
// quitting, sending, scrolling, history, and selecting and expanding
// blocks. The rest go to the input.
func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	m.notice = ""
	k := msg.String()
	now := time.Now()
	typing := now.Sub(m.lastKey) < approvalGuard
	m.lastKey = now

	if m.noting && len(m.pending) > 0 && k != "ctrl+c" {
		return m.handleNoteKey(msg)
	}
	if m.overlayText != "" && k == "esc" {
		m.closeOverlay()
		return nil
	}
	if (k == "ctrl+c" || k == "esc") && m.inTurn && m.turnCancel != nil {
		// A second Ctrl+C quits even if the turn hasn't wound down, e.g. a
		// stream that ignores cancellation.
		if k == "ctrl+c" && now.Sub(m.quitArmed) < quitWindow {
			m.quitting = true
			return tea.Quit
		}
		m.cancelTurn()
		if k == "ctrl+c" {
			m.quitArmed = now
			m.notice = "Cancelling · Ctrl+C again to exit"
		}
		return nil
	}
	if len(m.pending) > 0 {
		return m.handlePermissionKey(msg, typing)
	}
	if m.modal != nil {
		return m.handlePickerKey(msg)
	}
	if matches := m.suggestions(); len(matches) > 0 && m.overlayText == "" {
		if cmd, handled := m.handleCommandKey(k, matches); handled {
			return cmd
		}
	}

	switch k {
	case "ctrl+c":
		return m.interrupt(now)
	case "esc":
		m.back()
	case "enter":
		return m.submit()
	case "f1":
		m.showHelp()
	case "ctrl+end":
		m.autoScroll = true
		m.viewport.GotoBottom()
	case "right":
		if !m.acceptSuggestion() {
			return m.updateInput(msg)
		}
	case "alt+up", "alt+down":
		m.recallHistory(k == "alt+up")
	case "up", "down":
		// Move within a multi-line draft; step through history at its edges.
		if (k == "up" && m.cursorOnFirstRow()) || (k == "down" && m.cursorOnLastRow()) {
			m.recallHistory(k == "up")
			return nil
		}
		return m.updateInput(msg)
	case "alt+left", "alt+right":
		m.selectBlock(k == "alt+right")
	case "ctrl+o":
		m.expandSelected()
	case "ctrl+r":
		m.toggleLastReasoning()
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		return m.scroll(msg)
	default:
		return m.updateInput(msg)
	}
	return nil
}

// interrupt handles an idle Ctrl+C: clear the draft, else arm quitting,
// else quit, so one stray press never loses work.
func (m *Model) interrupt(now time.Time) tea.Cmd {
	switch {
	case m.input.Value() != "":
		m.input.SetValue("")
		m.applyLayout()
		m.notice = "Input cleared · Ctrl+C again to exit"
		m.quitArmed = now
	case now.Sub(m.quitArmed) < quitWindow:
		m.quitting = true
		return tea.Quit
	default:
		m.quitArmed = now
		m.notice = "Press Ctrl+C again to exit"
	}
	return nil
}

// back undoes the innermost view state on Esc; it never exits.
func (m *Model) back() {
	switch {
	case m.selectedBlock >= 0:
		m.selectedBlock = -1
		m.syncViewport()
	case m.suggestion != "":
		m.clearSuggestion()
	default:
		m.notice = "Ctrl+C twice to exit"
	}
}

// scroll moves whichever pane fills the screen: the overlay, the debug
// panel, else the transcript,
// following new output again once it reaches the bottom.
func (m *Model) scroll(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch {
	case m.overlayText != "":
		m.overlay, cmd = m.overlay.Update(msg)
		return cmd
	case m.debugFullscreen():
		m.debug, cmd = m.debug.Update(msg)
		return cmd
	}
	m.viewport, cmd = m.viewport.Update(msg)
	m.autoScroll = m.viewport.AtBottom()
	return cmd
}

// cursorOnFirstRow reports whether Up would leave the draft at its top.
func (m *Model) cursorOnFirstRow() bool {
	return m.input.Line() == 0 && m.input.LineInfo().RowOffset == 0
}

// cursorOnLastRow reports whether Down would leave the draft at its bottom.
func (m *Model) cursorOnLastRow() bool {
	info := m.input.LineInfo()
	return m.input.Line() == m.input.LineCount()-1 && info.RowOffset == info.Height-1
}

// recordHistory remembers a submitted input, skipping an immediate repeat.
func (m *Model) recordHistory(input string) {
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != input {
		m.inputHistory = append(m.inputHistory, input)
	}
	m.historyIndex = len(m.inputHistory)
	m.draft = ""
}

// recallHistory steps through submitted prompts and commands, keeping the
// unsent draft at the end of the list.
func (m *Model) recallHistory(older bool) {
	if len(m.inputHistory) == 0 {
		return
	}
	if m.historyIndex == len(m.inputHistory) {
		m.draft = m.input.Value()
	}
	if older {
		m.historyIndex = max(0, m.historyIndex-1)
	} else {
		m.historyIndex = min(len(m.inputHistory), m.historyIndex+1)
	}
	if m.historyIndex == len(m.inputHistory) {
		m.input.SetValue(m.draft)
	} else {
		m.input.SetValue(m.inputHistory[m.historyIndex])
	}
	// A recalled command doesn't open the command popup, so ↑/↓ keep
	// stepping through history; typing on opens it.
	m.cmdDismissed = m.input.Value()
	m.applyLayout()
}

// handlePermissionKey answers the front request. Letters typed while the
// user was already typing, or right after the prompt appeared, go to the
// draft instead: they were meant for it.
func (m *Model) handlePermissionKey(msg tea.KeyMsg, typing bool) tea.Cmd {
	hasty := typing || time.Since(m.pendingSince) < approvalGuard
	var d permission.Decision
	switch strings.ToLower(msg.String()) {
	case "y":
		d = permission.Allow
	case "a":
		d = permission.AllowAlways
	case "n":
		d = permission.Deny
	case "t":
		if !hasty {
			m.startNoting()
			return nil
		}
		return m.updateInput(msg)
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			return m.updateInput(msg)
		}
		var cmd tea.Cmd
		m.approval, cmd = m.approval.Update(msg)
		return cmd
	}
	if hasty {
		return m.updateInput(msg)
	}
	m.decide(d, "")
	return nil
}

// pruneStale drops approval requests whose call was cancelled while they
// waited, e.g. with its sub-agent, reporting whether there were any.
func (m *Model) pruneStale() bool {
	n := len(m.pending)
	m.pending = slices.DeleteFunc(m.pending, func(r PermissionRequestMsg) bool { return r.Context != nil && r.Context.Err() != nil })
	return len(m.pending) != n
}

// decide replies to the front request and moves on to the next one.
func (m *Model) decide(d permission.Decision, note string) {
	req := m.pending[0]
	req.Reply <- Answer{Decision: d, Note: note}
	m.pending = m.pending[1:]
	m.pendingSince = time.Now()
	m.approval.GotoTop()
	if req.Agent == "" { // sub-agent calls have no block in the chat
		m.answerPermission(req, d != permission.Deny)
	}
	m.applyLayout()
}

// startNoting sets the draft aside so the input can take a denial note.
func (m *Model) startNoting() {
	m.noting, m.noteDraft = true, m.input.Value()
	m.input.SetValue("")
	m.input.Placeholder = "what should wisp do instead? enter denies with this note, esc goes back"
	m.applyLayout()
}

// stopNoting restores the draft set aside by startNoting.
func (m *Model) stopNoting() {
	if !m.noting {
		return
	}
	m.noting = false
	m.input.SetValue(m.noteDraft)
	m.noteDraft = ""
	m.input.Placeholder = inputPlaceholder
}

// handleNoteKey edits the denial note: Enter denies with it, Esc goes back
// to the prompt.
func (m *Model) handleNoteKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.stopNoting()
		m.applyLayout()
		return nil
	case "enter":
		note := strings.TrimSpace(m.input.Value())
		m.stopNoting()
		m.decide(permission.Deny, note)
		return nil
	}
	return m.updateInput(msg)
}
