package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/antoniosarro/wisp/internal/core"
)

const (
	sidePanelWidth     = 36 // outer width of the right-hand column, border included
	minSplitWidth      = 76 // narrower terminals have no room for side panels
	minMascotChatWidth = 40 // narrower chat text runs under the mascot rather than squeeze beside it
)

// debugBeside and the rest say where the open panels go: in the side
// column, or, on a narrow terminal, in place of the transcript (debug) or
// nowhere but the chat's tool cards (tasks).
func (m *Model) debugBeside() bool     { return m.debugOpen && m.width >= minSplitWidth }
func (m *Model) debugFullscreen() bool { return m.debugOpen && m.width < minSplitWidth }
func (m *Model) todoBeside() bool {
	return m.todoOpen && len(m.todos) > 0 && m.width >= minSplitWidth
}

// sideWidth is how much of the terminal the right-hand column takes.
func (m *Model) sideWidth() int {
	if len(m.sidePanels()) > 0 {
		return sidePanelWidth
	}
	return 0
}

// sidePanel is one right-hand panel; render takes a height, 0 meaning fit
// the content.
type sidePanel struct {
	name   string
	render func(height int) string
}

// sidePanels lists the open right-hand panels, top to bottom.
func (m *Model) sidePanels() []sidePanel {
	var panels []sidePanel
	if m.todoBeside() {
		panels = append(panels, sidePanel{"tasks", m.todoPanel})
	}
	if m.agentsBeside() {
		panels = append(panels, sidePanel{"agents", m.agentsPanel})
	}
	if m.debugBeside() {
		panels = append(panels, sidePanel{"debug", func(h int) string {
			return renderDebugPanel(m.stats, m.opts, m.costSummary(), m.agentTokens(), h, sidePanelWidth)
		}})
	}
	return panels
}

// renderSideColumn stacks the panels, using the same heights as
// sideHeights but rendering each panel once: a panel shown at its natural
// height reuses the render that measured it.
func (m *Model) renderSideColumn(height int) string {
	panels := m.sidePanels()
	parts := make([]string, len(panels))
	left := height
	for i, p := range panels {
		if i == len(panels)-1 {
			parts[i] = p.render(left)
			break
		}
		natural := p.render(0)
		h := min(lipgloss.Height(natural), height/len(panels))
		parts[i] = natural
		if h != lipgloss.Height(natural) {
			parts[i] = p.render(h)
		}
		left -= h
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// sideHeights splits height among the open panels. Each panel but the
// last takes its content height, up to an equal share; the last fills what
// remains.
func (m *Model) sideHeights(height int) []int {
	panels := m.sidePanels()
	heights := make([]int, len(panels))
	left := height
	for i, p := range panels {
		h := left
		if i < len(panels)-1 {
			h = min(lipgloss.Height(p.render(0)), height/len(panels))
		}
		heights[i] = h
		left -= h
	}
	return heights
}

// sidePanelTop returns the screen row where the named panel starts, if open.
func (m *Model) sidePanelTop(name string) (int, bool) {
	heights := m.sideHeights(m.viewport.Height + 2)
	top := 0
	for i, p := range m.sidePanels() {
		if p.name == name {
			return top, true
		}
		top += heights[i]
	}
	return 0, false
}

// setTodos records the model's latest task list, opening the panel the
// first time a list appears.
func (m *Model) setTodos(args json.RawMessage) {
	var a struct {
		Todos []core.Todo `json:"todos"`
	}
	if json.Unmarshal(args, &a) != nil {
		return
	}
	// The panel follows the plan: it opens when one starts and closes
	// once every task in it is done. /todo still toggles it.
	switch {
	case allDone(a.Todos):
		m.todoOpen = false
	case len(a.Todos) > 0 && (len(m.todos) == 0 || allDone(m.todos)):
		m.todoOpen = true
	}
	m.todos = a.Todos
	m.applyLayout()
}

// allDone reports whether a non-empty task list has every task completed.
func allDone(todos []core.Todo) bool {
	for _, t := range todos {
		if t.Status != "completed" {
			return false
		}
	}
	return len(todos) > 0
}

// toggleTodo shows or hides the task panel.
func (m *Model) toggleTodo() {
	if len(m.todos) == 0 {
		m.notify("No task list yet; the model creates one with the todo tool.")
		return
	}
	m.todoOpen = !m.todoOpen
	m.applyLayout()
}

// todoPanel renders the task list; height <= 0 means fit the content.
func (m *Model) todoPanel(height int) string {
	inner := max(10, sidePanelWidth-4)
	done := 0
	var items strings.Builder
	for _, t := range m.todos {
		content := sanitize(t.Content) // the model wrote it
		mark, text := styleDim.Render("○"), content
		switch t.Status {
		case "completed":
			done++
			mark, text = styleToolOK.Render("✓"), styleDim.Render(content)
		case "in_progress":
			mark, text = styleSpinner.Render("◐"), styleToolText.Render(content)
			if m.inTurn {
				mark = styleSpinner.Render(strings.TrimSpace(m.spin.View()))
			}
		}
		// Wrap under the text, not the marker.
		body := lipgloss.NewStyle().Width(inner - 2).Render(text)
		items.WriteString(mark)
		items.WriteString(" ")
		items.WriteString(strings.ReplaceAll(body, "\n", "\n  "))
		items.WriteString("\n")
	}
	title := styleSplashHead.Render("Tasks") + " " + styleDim.Render(fmt.Sprintf("%d/%d", done, len(m.todos)))
	return renderSidePanel(title+"\n\n"+strings.TrimRight(items.String(), "\n"), height, sidePanelWidth)
}

// renderSidePanel frames content in a side panel of the given outer size;
// height <= 0 means fit the content. Content that doesn't fit is cut so
// the frame stays closed.
func renderSidePanel(content string, height, width int) string {
	content = lipgloss.NewStyle().Width(max(10, width-4)).Render(content)
	style := styleDebugPanel.Width(width - 2)
	if height <= 0 {
		return style.Render(content)
	}
	inner := max(1, height-2)
	if lines := strings.Split(content, "\n"); len(lines) > inner {
		content = strings.Join(lines[:inner], "\n")
	}
	return style.Height(inner).Render(content)
}
