package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const helpText = `Commands: /help, /context, /compact [FOCUS], /clear, /model [NAME], /resume [SESSION], /sessions, /debug, /todo, /agents, /back
Typing / lists the commands, and after /model or /resume their choices: ↑/↓ choose, Tab completes, Enter runs, Esc closes the list.
/model and /resume without an argument open a list to choose from: type to filter it.

Enter sends; Alt+Enter or Ctrl+J inserts a newline.
Up/Down (or Alt+Up/Down) recall sent prompts and commands. You can draft while a turn runs.
Esc or Ctrl+C cancels a running turn. When idle, Esc closes this view, leaves a
sub-agent chat, or clears a selection; Ctrl+C clears the input, and twice exits.
PgUp/PgDown scroll; Ctrl+End follows the latest output.
Alt+Left/Right selects transcript blocks; Ctrl+O expands/collapses, or opens a
sub-agent's own chat from its box.
Ctrl+R toggles the latest reasoning block.
Mouse: hover marks the block a click will hit; click a tool or reasoning box to
expand/collapse it, or an agent box to open its chat; drag over text to copy it.
Ctrl+Y copies the selected block to the system clipboard (over SSH, or with no
clipboard tool, through the terminal with OSC 52).
Approvals: y allows, a always allows matching calls this session, n denies,
t denies with a note telling wisp what to do instead, Esc cancels the turn;
the key hints can also be clicked.
Arrows/PgUp/PgDown scroll details. Keys typed while you were typing go to your draft.
/model lists models: arrows choose, Enter switches.
/clear starts a new session; /resume or /sessions lists saved ones to continue.
/todo shows or hides the task panel, which opens when the model first plans with the todo tool.
/compact summarizes the conversation so far to free context, keeping recent messages
verbatim; any text after it says what the summary should keep in detail. wisp also
compacts on its own when the context fills up.
/agents shows or hides the sub-agent panel, which opens when the first sub-agent starts;
click a run in it to open that sub-agent's chat.
Ctrl+B or /back returns to the main chat from a sub-agent's.
Side panels stack on the right; on narrow terminals the tasks stay in the chat and /debug replaces the transcript.
Start wisp with --suggest to get a suggested next message after each reply (→ accepts it).
Set WISP_THEME=light for a light terminal palette.`

// Notices shown from more than one place.
const (
	msgChooseModel     = "Choose a model first: /model NUMBER or /model NAME (/model lists them)."
	msgBusySwitchModel = "Cancel or finish the current turn before switching models."
	msgNoModelSwitch   = "Model switching is unavailable for this provider."
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
	case "model":
		if m.inTurn {
			m.notify(msgBusySwitchModel)
			return nil, true
		}
		return m.listModels(strings.Join(args, " ")), true
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

// showHelp shows the keys and commands over the transcript.
func (m *Model) showHelp() {
	m.modal = nil
	m.showOverlay(helpText)
}

// showOverlay shows reference text over the transcript until Esc, so it
// doesn't pile up in the chat.
func (m *Model) showOverlay(text string) {
	m.overlayText = text
	m.overlay.GotoTop()
	m.applyLayout()
}

// closeOverlay hides the overlay, if one is shown.
func (m *Model) closeOverlay() {
	if m.overlayText != "" {
		m.overlayText = ""
		m.applyLayout()
	}
}
