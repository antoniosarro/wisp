package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// applyLayout recomputes component sizes from the terminal size and the
// input's height.
func (m *Model) applyLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.input.SetWidth(max(1, m.width-6))
	m.input.SetHeight(min(5, max(1, m.height-5), max(1, m.input.LineCount()))) // leave room for a 1-line chat box
	boxHeight := max(0, m.height-lipgloss.Height(m.footer())-2)                // chat box border
	if !m.ready {
		m.viewport = viewport.New(m.chatWidth(), boxHeight)
		m.ready = true
	} else {
		m.viewport.Width = m.chatWidth()
		m.viewport.Height = boxHeight
	}
	m.syncViewport()
}

// chatWidth is the transcript's content width inside the chat box.
func (m *Model) chatWidth() int {
	return max(1, m.width-6)
}

// syncViewport re-renders the transcript into the viewport.
func (m *Model) syncViewport() {
	if !m.ready {
		return
	}
	m.dirty = false
	m.viewport.SetContent(m.render())
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

// render builds the transcript, padded toward the bottom of the viewport
// so a short chat sits next to the input.
func (m *Model) render() string {
	if len(m.entries) == 0 {
		return ""
	}
	w := m.chatWidth()
	// Hardwrap as well: a word longer than the line would overflow the box.
	chat := ansi.Hardwrap(m.entries.render(w, m.spin.View()), w, true)
	pad := max(0, m.viewport.Height-(strings.Count(chat, "\n")+1))
	return strings.Repeat("\n", pad) + chat
}

// View draws the chat box over the input.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "loading...\n"
	}
	if m.width < 20 || m.height < 8 {
		return fitView("Terminal too small; resize to 20×8", m.width, m.height)
	}
	return fitView(m.chatBox(max(1, m.width-2))+"\n"+m.footer(), m.width, m.height)
}

// fitView clips s to the terminal, so an oversized frame can't scroll it.
func fitView(s string, w, h int) string {
	return lipgloss.NewStyle().MaxWidth(max(1, w)).MaxHeight(max(1, h)).Render(s)
}

// footer is what sits below the chat box: the input.
func (m *Model) footer() string {
	return boxed(styleInputBox, m.width, m.input.View())
}

// chatBox frames the transcript, with any notice in its bottom border.
func (m *Model) chatBox(width int) string {
	box := styleChatBox.Width(width).Render(m.viewport.View())
	label := m.notice
	switch {
	case label != "":
	case m.inTurn:
		label = "esc to interrupt"
	case !m.autoScroll:
		label = "↓ pgdown for latest"
	}
	return withBorderLabel(box, label)
}

// withBorderLabel writes label into the bottom border of a rounded box.
func withBorderLabel(box, label string) string {
	if label == "" {
		return box
	}
	lines := strings.Split(box, "\n")
	last := len(lines) - 1
	lines[last] = borderLine("╰", "╯", styleDim.Render(label), lipgloss.Width(lines[last]), styleBorderLine)
	return strings.Join(lines, "\n")
}

// borderLine draws a width-wide horizontal border with label embedded.
func borderLine(start, end, label string, width int, border lipgloss.Style) string {
	label = ansi.Truncate(label, max(0, width-6), "…")
	used := 5 + ansi.StringWidth(label) // "╰─ " + label + " " + end
	return border.Render(start+"─ ") + label + border.Render(" "+strings.Repeat("─", max(0, width-used))) + border.Render(end)
}
