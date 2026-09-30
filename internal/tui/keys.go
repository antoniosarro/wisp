package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// quitWindow is how soon a second Ctrl+C must follow the first to exit.
const quitWindow = 2 * time.Second

// handleKey handles a key press: cancelling and quitting, sending,
// scrolling, history, and selecting and expanding blocks. The rest go to
// the input.
func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	m.notice = ""
	k := msg.String()
	now := time.Now()

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

	switch k {
	case "ctrl+c":
		return m.interrupt(now)
	case "esc":
		m.back()
	case "enter":
		return m.submit()
	case "ctrl+end":
		m.autoScroll = true
		m.viewport.GotoBottom()
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
	default:
		m.notice = "Ctrl+C twice to exit"
	}
}

// scroll moves the transcript, following new output again once it
// reaches the bottom.
func (m *Model) scroll(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
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

// recallHistory steps through submitted prompts, keeping the unsent draft
// at the end of the list.
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
	m.applyLayout()
}
