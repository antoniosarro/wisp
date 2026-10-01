package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// command is a slash command as the suggestion popup lists it. Arguments
// in <> are required, in [] optional.
type command struct{ name, args, desc string }

// commands are the slash commands, in the order the popup lists them.
var commands = []command{
	{"help", "", "keys and commands"},
	{"context", "", "what fills the context window"},
	{"compact", "[focus]", "summarize the conversation to free context"},
	{"clear", "", "start a new session; this one stays saved"},
	{"model", "[name]", "switch model (lists them without a name)"},
	{"effort", "[level]", "set the reasoning effort (lists the model's levels without one)"},
	{"resume", "[session]", "continue a saved session (lists them without one)"},
	{"sessions", "", "list saved sessions to resume"},
	{"debug", "", "show or hide the debug panel"},
	{"todo", "", "show or hide the task panel"},
	{"agents", "", "show or hide the sub-agent panel"},
	{"back", "", "return to the main chat"},
}

// suggestion is one row of the popup: a command, or an argument for one.
type suggestion struct {
	fill  string // the input it completes to
	label string
	hint  string // dim, after the label
	desc  string
	run   bool // Enter runs it rather than completing it
}

// maxPopupRows bounds the popup on a tall terminal.
const maxPopupRows = 6

// popupRowLimit is how many rows the popup may take: maxPopupRows, fewer
// when the terminal is short, so the chat box keeps a line and its borders
// above the popup and the input box.
func (m *Model) popupRowLimit() int {
	input := m.input.Height() + 2 // with its border
	return max(0, min(maxPopupRows, m.height-input-3))
}

// suggestions lists what the input could be completing: a "/word" being
// typed matches commands, and "/model ", "/effort ", or "/resume "
// followed by a word matches their arguments. Prefix matches come first,
// then the rest that contain the word. Nothing is suggested once the popup
// was dismissed for this input.
func (m *Model) suggestions() []suggestion {
	v := m.input.Value()
	if !strings.HasPrefix(v, "/") || strings.Contains(v, "\n") || v == m.cmdDismissed || len(m.pending) > 0 {
		return nil
	}
	name, arg, hasArg := strings.Cut(v[1:], " ")
	if hasArg {
		if strings.Contains(arg, " ") {
			return nil
		}
		return m.argSuggestions(name, strings.ToLower(arg))
	}
	word := strings.ToLower(name)
	var prefix, contains []suggestion
	for _, c := range commands {
		s := suggestion{fill: "/" + c.name, label: "/" + c.name, hint: c.args, desc: c.desc, run: !strings.HasPrefix(c.args, "<")}
		if c.args != "" {
			s.fill += " "
		}
		switch {
		case strings.HasPrefix(c.name, word):
			prefix = append(prefix, s)
		case strings.Contains(c.name, word):
			contains = append(contains, s)
		}
	}
	return append(prefix, contains...)
}

// argSuggestions lists the models, effort levels, or sessions the
// argument could name.
// They are listed once per popup and cached, since the popup renders on
// every frame.
func (m *Model) argSuggestions(cmd, word string) []suggestion {
	if m.argCache == nil || m.argCacheFor != cmd {
		var items []pickerItem
		switch cmd {
		case "model":
			items = m.modelItems(m.knownModels)
		case "resume":
			items = m.sessionItems()
		case "effort":
			items = m.effortItems()
		default:
			return nil
		}
		m.argCache, m.argCacheFor = items, cmd
	}
	var prefix, contains []suggestion
	for _, it := range m.argCache {
		s := suggestion{fill: "/" + cmd + " " + it.value, label: it.title, desc: it.detail, run: true}
		if it.current {
			s.hint = "current"
		}
		switch {
		case strings.HasPrefix(strings.ToLower(it.title), word) || strings.HasPrefix(strings.ToLower(it.value), word):
			prefix = append(prefix, s)
		case strings.Contains(strings.ToLower(it.title+" "+it.value), word):
			contains = append(contains, s)
		}
	}
	return append(prefix, contains...)
}

// handleCommandKey handles the keys the popup takes while it is open:
// ↑/↓ choose, Tab completes, Enter runs, Esc dismisses. handled is false
// for keys it leaves to the input.
func (m *Model) handleCommandKey(k string, list []suggestion) (cmd tea.Cmd, handled bool) {
	m.cmdIndex = min(m.cmdIndex, len(list)-1)
	s := list[m.cmdIndex]
	switch k {
	case "up", "down":
		step := map[string]int{"up": -1, "down": 1}[k]
		m.cmdIndex = (m.cmdIndex + step + len(list)) % len(list)
	case "tab":
		m.setInput(s.fill)
	case "enter":
		if !s.run {
			m.setInput(s.fill)
			return nil, true
		}
		m.input.SetValue(strings.TrimSpace(s.fill))
		return m.submit(), true
	case "esc":
		m.cmdDismissed = m.input.Value()
	default:
		return nil, false
	}
	return nil, true
}

// setInput replaces the input with v, the cursor at its end.
func (m *Model) setInput(v string) {
	m.input.SetValue(v)
	m.input.CursorEnd()
}

// fitPopup relays out when the popup's height changed, since it takes its
// rows from the chat box, and drops the argument cache once it closes.
func (m *Model) fitPopup() {
	list := m.suggestions()
	if len(list) == 0 {
		m.argCache = nil
	}
	if m.cmdIndex >= len(list) {
		m.cmdIndex = 0
	}
	if h := min(len(list), m.popupRowLimit()); h != m.popupRows {
		m.popupRows = h
		m.applyLayout()
	}
}

// renderCommandPopup lists the suggestions above the input, the chosen one
// marked, scrolled to keep it in view.
func (m *Model) renderCommandPopup() string {
	list := m.suggestions()
	if len(list) == 0 {
		return ""
	}
	width := func(s suggestion) int { // label and hint, as shown
		if s.hint == "" {
			return ansi.StringWidth(sanitize(s.label))
		}
		return ansi.StringWidth(sanitize(s.label)) + 1 + len(s.hint)
	}
	labelWidth := 0
	for _, s := range list {
		labelWidth = max(labelWidth, width(s))
	}
	labelWidth = min(labelWidth, max(10, m.width/2))
	limit := m.popupRowLimit()
	first := max(0, m.cmdIndex-limit+1)
	var rows []string
	for i := first; i < min(len(list), first+limit); i++ {
		s := list[i]
		marker, style := "  ", styleToolText
		if i == m.cmdIndex {
			marker, style = styleSpinner.Render("› "), styleAnswerPrefix.Bold(true)
		}
		label := style.Render(sanitize(s.label))
		if s.hint != "" {
			label += " " + styleDim.Render(s.hint)
		}
		pad := strings.Repeat(" ", max(1, labelWidth-width(s)+2))
		rows = append(rows, ansi.Truncate(fmt.Sprintf(" %s%s%s %s", marker, label, pad, styleDim.Render(sanitize(s.desc))), max(1, m.width), "…"))
	}
	return strings.Join(rows, "\n")
}
