package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Transcript content starts inside the chat box's border and padding.
const (
	chatOriginX = 2
	chatOriginY = 1
)

// textPos is a cell position in the rendered transcript.
type textPos struct{ line, col int }

// before reports whether p comes before q in reading order.
func (p textPos) before(q textPos) bool {
	return p.line < q.line || (p.line == q.line && p.col < q.col)
}

// textSelection tracks a mouse drag over the transcript.
type textSelection struct {
	anchor, head textPos
	pressed      bool
	dragged      bool
}

// visible reports whether the selection is highlighted: a click that
// never moved selects nothing.
func (s textSelection) visible() bool { return s.dragged }

// bounds returns the selection's start and end in reading order.
func (s textSelection) bounds() (textPos, textPos) {
	if s.head.before(s.anchor) {
		return s.head, s.anchor
	}
	return s.anchor, s.head
}

// handleMouse highlights the block under the pointer, scrolls on the
// wheel, toggles a block on click, and copies dragged text on release.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if len(m.pending) > 0 {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && !m.noting {
			if key := m.approvalKeyAt(msg.X, msg.Y); key != "" {
				m.clickApproval(key)
				return nil
			}
		}
		// The wheel scrolls what is under the pointer: the request's details
		// in the approval box, the chat above it.
		if tea.MouseEvent(msg).IsWheel() && msg.Y < m.height-strings.Count(m.footer(), "\n")-1 {
			return m.scroll(msg)
		}
		var cmd tea.Cmd
		m.approval, cmd = m.approval.Update(msg)
		return cmd
	}
	if m.helpOpen {
		if tea.MouseEvent(msg).IsWheel() {
			return m.scroll(msg)
		}
		return nil
	}
	m.mouseX, m.mouseY = msg.X, msg.Y
	if tea.MouseEvent(msg).IsWheel() || m.debugFullscreen() {
		cmd := m.scroll(msg)
		m.updateHover()
		return cmd
	}
	if msg.Action == tea.MouseActionMotion && !m.sel.pressed {
		m.updateHover()
		return nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.X >= m.width-m.sideWidth() {
		if runID, ok := m.agentRunAt(msg.Y); ok {
			m.openRun(runID)
		}
		return nil
	}

	pos, inside := m.transcriptPos(msg.X, msg.Y)
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft || !inside {
			return nil
		}
		m.sel = textSelection{anchor: pos, head: pos, pressed: true}
		m.refreshViewport()
	case tea.MouseActionMotion:
		if !m.sel.pressed {
			return nil
		}
		m.autoScroll = false
		m.followDrag(msg.Y)
		pos, _ = m.transcriptPos(msg.X, msg.Y)
		m.sel.head = pos
		m.sel.dragged = m.sel.dragged || pos != m.sel.anchor
		m.refreshViewport()
	case tea.MouseActionRelease:
		if !m.sel.pressed {
			return nil
		}
		m.sel.pressed = false
		if !m.sel.dragged {
			line := m.sel.anchor.line
			m.sel = textSelection{}
			m.clickLine(line)
			return nil
		}
		if text := m.selectedText(); text != "" {
			return copyCmd(text, fmt.Sprintf("Copied %d characters", utf8.RuneCountInString(text)))
		}
	}
	return nil
}

// transcriptPos maps a screen cell to a transcript position, clamped to
// the visible viewport; inside reports whether it was within it.
func (m *Model) transcriptPos(x, y int) (textPos, bool) {
	row, col := y-chatOriginY, x-chatOriginX
	inside := row >= 0 && row < m.viewport.Height && col >= 0 && col < m.viewport.Width
	row = min(max(row, 0), max(m.viewport.Height-1, 0))
	return textPos{line: m.viewport.YOffset + row, col: max(col, 0)}, inside
}

// followDrag scrolls when a drag leaves the top or bottom of the viewport.
func (m *Model) followDrag(y int) {
	switch row := y - chatOriginY; {
	case row < 0:
		m.viewport.ScrollUp(1)
	case row >= m.viewport.Height:
		m.viewport.ScrollDown(1)
	}
}

// clickLine activates and selects the interactive block at a transcript
// line; clicking anything else clears the selection.
func (m *Model) clickLine(line int) {
	if i := m.blockAt(line); i >= 0 && m.interactive(i) {
		m.selectedBlock = i
		m.activate(i)
		return
	}
	m.selectedBlock = -1
	m.syncViewport()
}

// updateHover marks the block under the pointer.
func (m *Model) updateHover() {
	i := -1
	if pos, inside := m.transcriptPos(m.mouseX, m.mouseY); inside && !m.debugFullscreen() {
		if j := m.blockAt(pos.line); j >= 0 && m.interactive(j) {
			i = j
		}
	}
	if i != m.hoverBlock {
		m.hoverBlock = i
		m.syncViewport()
	}
}

// blockAt returns the shown block at a transcript line, or -1.
func (m *Model) blockAt(line int) int {
	for i, r := range m.blockLines {
		if line >= r.start && line < r.end {
			return i
		}
	}
	return -1
}

// clearSelection drops a finished drag highlight.
func (m *Model) clearSelection() {
	if m.sel != (textSelection{}) {
		m.sel = textSelection{}
		m.refreshViewport()
	}
}

// highlightSelection reverses the selected cells of content.
func (m *Model) highlightSelection(content string) string {
	start, end := m.sel.bounds()
	lines := strings.Split(content, "\n")
	for l := start.line; l <= end.line && l < len(lines); l++ {
		from, to := lineSpan(lines[l], l, start, end)
		if from < to {
			line := lines[l]
			lines[l] = ansi.Cut(line, 0, from) + styleSelection.Render(ansi.Strip(ansi.Cut(line, from, to))) + ansi.TruncateLeft(line, to, "")
		}
	}
	return strings.Join(lines, "\n")
}

// lineSpan returns the selected cell range [from, to) of line l.
func lineSpan(line string, l int, start, end textPos) (int, int) {
	from, to := 0, ansi.StringWidth(line)
	if l == start.line {
		from = start.col
	}
	if l == end.line {
		to = min(to, end.col+1)
	}
	return from, to
}

// selectedText returns the dragged text without card borders or the
// transcript's left margin.
func (m *Model) selectedText() string {
	start, end := m.sel.bounds()
	lines := strings.Split(m.content, "\n")
	var out []string
	for l := start.line; l <= end.line && l < len(lines); l++ {
		plain := ansi.Strip(lines[l])
		from, to := lineSpan(plain, l, start, end)
		if line, ok := cleanCopiedLine(ansi.Cut(plain, from, to)); ok {
			out = append(out, line)
		}
	}
	// A line cut mid-way has no margin left, so it doesn't count toward the indent.
	return strings.Trim(dedent(out, start.col > 0), "\n")
}

// cleanCopiedLine strips box-drawing borders; ok is false for pure border rows.
func cleanCopiedLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed != "" && strings.Trim(trimmed, "╭╮╰╯─") == "" {
		return "", false
	}
	if rest, ok := strings.CutPrefix(strings.TrimLeft(line, " "), "│"); ok {
		line = strings.TrimPrefix(rest, " ")
	}
	line = strings.TrimRight(line, " ")
	line = strings.TrimSuffix(line, "│")
	return strings.TrimRight(line, " "), true
}

// dedent removes the indentation shared by all non-blank lines, ignoring
// the first line if skipFirst.
func dedent(lines []string, skipFirst bool) string {
	indent := -1
	for i, l := range lines {
		if skipFirst && i == 0 && len(lines) > 1 {
			continue
		}
		if strings.TrimSpace(l) != "" {
			n := len(l) - len(strings.TrimLeft(l, " "))
			if indent < 0 || n < indent {
				indent = n
			}
		}
	}
	for i, l := range lines {
		n := len(l) - len(strings.TrimLeft(l, " "))
		lines[i] = l[min(max(indent, 0), n):]
	}
	return strings.Join(lines, "\n")
}

// approvalHints are the approval box's clickable key hints.
var approvalHints = []struct{ label, key string }{
	{"y allow", "y"}, {"a always allow", "a"}, {"n deny", "n"}, {"t deny with note", "t"}, {"esc cancel turn", "esc"},
}

// approvalKeyAt returns the key whose hint is at screen cell (x, y), or "".
// The hints sit on the last one or two lines inside the approval box, at
// the bottom of the screen; only those lines are searched, so text in the
// request's details can't act as a button.
func (m *Model) approvalKeyAt(x, y int) string {
	lines := strings.Split(m.footer(), "\n")
	row := y - (m.height - len(lines))
	if row < len(lines)-3 || row > len(lines)-2 {
		return ""
	}
	line := ansi.Strip(lines[row])
	for _, h := range approvalHints {
		if i := strings.Index(line, h.label); i >= 0 {
			start := ansi.StringWidth(line[:i])
			if x >= start && x < start+ansi.StringWidth(h.label) {
				return h.key
			}
		}
	}
	return ""
}

// clipboardResultMsg reports how a copy went.
type clipboardResultMsg struct {
	err    error
	notice string
}

// terminalOut is where OSC 52 clipboard requests are written.
var terminalOut io.Writer = os.Stdout

// copyCmd copies text to the system clipboard. Over SSH that clipboard is
// the remote machine's, and it may be missing entirely, so the terminal is
// asked instead with OSC 52, which also passes through tmux and screen.
func copyCmd(text, notice string) tea.Cmd {
	return func() tea.Msg {
		overSSH := os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != ""
		if !overSSH && clipboard.WriteAll(text) == nil {
			return clipboardResultMsg{notice: notice}
		}
		seq := osc52.New(text)
		switch {
		case os.Getenv("TMUX") != "":
			seq = seq.Tmux()
		case strings.HasPrefix(os.Getenv("TERM"), "screen"):
			seq = seq.Screen()
		}
		if _, err := seq.WriteTo(terminalOut); err != nil {
			return clipboardResultMsg{err: err}
		}
		return clipboardResultMsg{notice: notice + " via the terminal (OSC 52)"}
	}
}
