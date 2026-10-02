package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// helpSection is a titled group of help rows: what to press or type, and
// what it does. A row without a key is a note across the whole width.
type helpSection struct {
	title string
	rows  [][2]string
}

// helpSections follow the commands (from commands) in the help.
var helpSections = []helpSection{
	{"Input", [][2]string{
		{"enter", "send, or run a /command"},
		{"alt+enter, ctrl+j", "insert a newline"},
		{"↑/↓, alt+↑/↓", "recall sent prompts and commands; you can draft while a turn runs"},
		{"/", "list the commands, and after /model, /effort, or /resume their choices: ↑/↓ choose, tab completes, enter runs, esc closes"},
		{"→", "accept the suggested next message (start wisp with --suggest)"},
		{"esc, ctrl+c", "cancel a running turn"},
		{"ctrl+c", "when idle, clear the input; twice exits"},
	}},
	{"Transcript", [][2]string{
		{"pgup/pgdown", "scroll"},
		{"ctrl+end", "follow the latest output"},
		{"alt+←/→", "select a block"},
		{"ctrl+o", "expand or collapse the selected block, or open a sub-agent's chat from its box"},
		{"ctrl+r", "toggle the latest reasoning block"},
		{"ctrl+y", "copy the selected block to the clipboard (through the terminal with OSC 52 over SSH or without a clipboard tool)"},
		{"esc", "leave a sub-agent's chat, or clear a selection"},
		{"ctrl+b", "return to the main chat from a sub-agent's"},
		{"mouse", "hover marks the block a click will hit; click a tool or reasoning box to expand it, or an agent box to open its chat; drag over text to copy it"},
	}},
	{"Approvals", [][2]string{
		{"y", "allow"},
		{"a", "always allow matching calls this session"},
		{"n", "deny"},
		{"t", "deny with a note telling wisp what to do instead"},
		{"esc", "cancel the turn"},
		{"↑/↓", "scroll the details; the wheel over the box does too"},
		{"pgup/pgdown", "scroll the chat, as does the wheel over it"},
		{"", "The key hints can also be clicked. Keys typed while you were typing go to your draft."},
	}},
	{"Notes", [][2]string{
		{"", "/model, /effort, and /resume without an argument open a list: type to filter it. The effort levels are the model's, as the endpoint reports them; none turns reasoning off."},
		{"", "/compact keeps recent messages verbatim; text after it says what the summary should keep in detail. wisp also compacts on its own when the context fills up."},
		{"", "The task and sub-agent panels open on their own when first needed; click a run in the sub-agent panel to open its chat."},
		{"", "Side panels stack on the right; on narrow terminals the tasks stay in the chat and /debug replaces the transcript."},
		{"", "Set WISP_THEME=light for a light terminal palette."},
	}},
}

// Notices shown from more than one place.
const (
	msgChooseModel     = "Choose a model first: /model NUMBER or /model NAME (/model lists them)."
	msgBusySwitchModel = "Cancel or finish the current turn before switching models."
	msgNoModelSwitch   = "Model switching is unavailable for this provider."
	msgBusyEffort      = "Cancel or finish the current turn before changing the reasoning effort."
)

// runCommand handles a "/name args..." input; ok is false for unknown commands.
func (m *Model) runCommand(input string) (cmd tea.Cmd, ok bool) {
	fields := strings.Fields(strings.TrimPrefix(input, "/"))
	if len(fields) == 0 {
		return nil, false
	}
	args := fields[1:]
	switch fields[0] {
	case "debug":
		m.toggleDebug()
	case "todo":
		m.toggleTodo()
	case "agents":
		m.toggleAgents()
	case "back", "main":
		m.viewMain()
	case "help":
		m.showHelp()
	case "sessions":
		m.pickSession()
	case "resume":
		if len(args) == 0 {
			m.pickSession()
		} else {
			m.resumeSession(args[0])
		}
	case "clear":
		m.clearSession()
	case "session-rename":
		m.renameSession(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), "/session-rename")))
	case "model":
		if m.inTurn {
			m.notify(msgBusySwitchModel)
			return nil, true
		}
		return m.listModels(strings.Join(args, " ")), true
	case "effort":
		if len(args) == 0 {
			m.pickEffort()
		} else {
			return m.setEffort(strings.ToLower(args[0])), true
		}
	case "compact":
		m.startCompact(strings.Join(args, " "))
	case "context":
		m.showContext()
	default:
		return nil, false
	}
	return nil, true
}

// notify appends an informational block to the transcript. Unlike a
// prompt, it doesn't pull a reader who scrolled up back to the bottom; the
// border's "ctrl+end for latest" hint points at it.
func (m *Model) notify(text string) {
	m.blocks = append(m.blocks, block{kind: blockNotice, text: text})
	m.applyLayout()
}

// toggleDebug shows or hides the debug panel.
func (m *Model) toggleDebug() {
	m.debugOpen = !m.debugOpen
	m.applyLayout()
}

// showHelp opens the keys and commands in a modal over the chat.
func (m *Model) showHelp() {
	m.modal = nil
	m.helpOpen = true
	m.help.GotoTop()
	m.applyLayout()
}

// closeHelp hides the help, if it's open.
func (m *Model) closeHelp() {
	if m.helpOpen {
		m.helpOpen = false
		m.applyLayout()
	}
}

// helpWidth is the help modal's outer width.
func (m *Model) helpWidth() int {
	return min(100, max(24, m.chatWidth()-4))
}

// renderHelp lays the help out to width: per section, its keys in one
// column and what they do wrapped beside them.
func renderHelp(width int) string {
	cmds := helpSection{title: "Commands"}
	for _, c := range commands {
		cmds.rows = append(cmds.rows, [2]string{strings.TrimSpace("/" + c.name + " " + c.args), c.desc})
	}
	sections := append([]helpSection{cmds}, helpSections...)
	keyWidth := 0
	for _, s := range sections {
		for _, r := range s.rows {
			keyWidth = max(keyWidth, ansi.StringWidth(r[0]))
		}
	}
	keyWidth = min(keyWidth, width/3)
	wrap := func(text string, w int) []string {
		w = max(1, w)
		return strings.Split(ansi.Hardwrap(ansi.Wrap(text, w, ""), w, true), "\n")
	}
	var b strings.Builder
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(styleApprovalTitle.Render(s.title) + "\n")
		for _, r := range s.rows {
			if r[0] == "" {
				for _, l := range wrap(r[1], width-2) {
					b.WriteString("  " + l + "\n")
				}
				continue
			}
			indent := strings.Repeat(" ", keyWidth+4)
			key := "  " + styleAnswerPrefix.Render(r[0])
			if w := ansi.StringWidth(r[0]); w > keyWidth { // its own line
				b.WriteString(key + "\n")
				key = indent
			} else {
				key += strings.Repeat(" ", keyWidth-w+2)
			}
			for j, l := range wrap(r[1], width-keyWidth-4) {
				if j > 0 {
					key = indent
				}
				b.WriteString(key + l + "\n")
			}
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// overlayHelp centers the help modal over main: a title, the scrolling
// body, and its keys.
func (m *Model) overlayHelp(main string) string {
	if !m.helpOpen {
		return main
	}
	inner := m.helpWidth() - 4
	title := styleToolText.Render("Help")
	if !(m.help.AtTop() && m.help.AtBottom()) {
		pct := fmt.Sprintf("%d%%", int(m.help.ScrollPercent()*100))
		title += strings.Repeat(" ", max(1, inner-4-len(pct))) + styleDim.Render(pct)
	}
	body := title + "\n\n" + m.help.View() + "\n\n" + styleDim.Render("↑/↓ pgup/pgdown scroll · esc to close")
	box := stylePermissionBox.Width(inner + 2).Render(body)
	top := max(0, (lipgloss.Height(main)-lipgloss.Height(box))/2)
	left := max(0, (lipgloss.Width(main)-lipgloss.Width(box))/2)
	return placeOver(main, box, top, left)
}
