package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// pickerItem is one choice in the picker.
type pickerItem struct {
	value   string // what pick receives
	title   string
	detail  string
	current bool // the model or session in use
}

// picker is a modal list over the chat: typing filters it, ↑/↓ choose,
// Enter picks, Esc closes.
type picker struct {
	title  string
	items  []pickerItem
	filter string
	index  int // into visible()
	pick   func(value string) tea.Cmd
}

// visible is the items matching the filter, in their order.
func (p *picker) visible() []pickerItem {
	if p.filter == "" {
		return p.items
	}
	f := strings.ToLower(p.filter)
	var out []pickerItem
	for _, it := range p.items {
		if strings.Contains(strings.ToLower(it.title+" "+it.detail+" "+it.value), f) {
			out = append(out, it)
		}
	}
	return out
}

// openPicker shows p with the current item chosen.
func (m *Model) openPicker(p *picker) {
	for i, it := range p.items {
		if it.current {
			p.index = i
		}
	}
	m.closeHelp()
	m.modal = p
}

// pickerRows is how many items the modal shows at once: most of the
// chat's height, leaving room for its title, filter, and keys.
func (m *Model) pickerRows() int {
	return max(3, min(20, m.viewport.Height-7))
}

// handlePickerKey takes every key while the picker is open.
func (m *Model) handlePickerKey(msg tea.KeyMsg) tea.Cmd {
	p := m.modal
	items := p.visible()
	move := func(delta int) {
		if len(items) > 0 {
			p.index = min(max(p.index+delta, 0), len(items)-1)
		}
	}
	switch k := msg.String(); k {
	case "esc", "ctrl+c":
		m.modal = nil
	case "up", "ctrl+p":
		move(-1)
	case "down", "ctrl+n":
		move(1)
	case "pgup":
		move(-m.pickerRows())
	case "pgdown":
		move(m.pickerRows())
	case "home":
		move(-len(items))
	case "end":
		move(len(items))
	case "enter":
		if len(items) == 0 {
			return nil
		}
		m.modal = nil
		return p.pick(items[p.index].value)
	case "backspace":
		if p.filter != "" {
			p.filter = string([]rune(p.filter)[:len([]rune(p.filter))-1])
			p.index = 0
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			p.filter += sanitize(string(msg.Runes)) // a paste can bring escape sequences
			p.index = 0
		}
	}
	return nil
}

// renderPicker draws the modal: title and count, the filter, the items
// around the chosen one, and the keys.
func (m *Model) renderPicker(width int) string {
	p := m.modal
	items := p.visible()
	inner := max(20, width-4)
	var b strings.Builder
	count := fmt.Sprintf("%d of %d", len(items), len(p.items))
	gap := max(1, inner-ansi.StringWidth(p.title)-len(count))
	b.WriteString(styleToolText.Render(p.title))
	b.WriteString(strings.Repeat(" ", gap))
	b.WriteString(styleDim.Render(count))
	b.WriteString("\n")
	if p.filter == "" {
		b.WriteString(styleDim.Render("⌕ type to filter"))
		b.WriteString("\n\n")
	} else {
		b.WriteString("⌕ ")
		b.WriteString(p.filter)
		b.WriteString(styleSpinner.Render("▏"))
		b.WriteString("\n\n")
	}

	rows := m.pickerRows()
	first := min(max(0, p.index-rows/2), max(0, len(items)-rows))
	titleWidth := 0
	for _, it := range items {
		titleWidth = max(titleWidth, ansi.StringWidth(sanitize(it.title)))
	}
	titleWidth = min(titleWidth, inner*3/5)
	for i := first; i < min(len(items), first+rows); i++ {
		it := items[i]
		marker, style := "  ", styleToolText.UnsetBold()
		if i == p.index {
			marker, style = styleSpinner.Render("› "), styleAnswerPrefix.Bold(true)
		}
		// Titles and details come from the endpoint and the session store.
		title := ansi.Truncate(sanitize(it.title), titleWidth, "…")
		line := marker + style.Render(title) + strings.Repeat(" ", titleWidth-ansi.StringWidth(title)+2) + styleDim.Render(sanitize(it.detail))
		if it.current {
			line += " " + styleToolOK.Render("● current")
		}
		b.WriteString(ansi.Truncate(line, inner, "…"))
		b.WriteString("\n")
	}
	if len(items) == 0 {
		b.WriteString(styleDim.Render("  nothing matches"))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styleDim.Render("↑/↓ choose · enter select · esc close"))
	return stylePermissionBox.Width(inner + 2).Render(b.String())
}

// placeOver draws box over base with its top-left corner at row top and
// column left, leaving the rest of base as it was.
func placeOver(base, box string, top, left int) string {
	lines := strings.Split(base, "\n")
	for i, row := range strings.Split(box, "\n") {
		if top+i < 0 || top+i >= len(lines) {
			continue
		}
		l := lines[top+i]
		// A wide character straddling either edge goes whole; pad its cells
		// back, so the box and the rest of the line keep their columns.
		end, rest := left+ansi.StringWidth(row), ansi.StringWidth(l)-left-ansi.StringWidth(row)
		before := ansi.Truncate(l, left, "")
		before += strings.Repeat(" ", max(0, left-ansi.StringWidth(before)))
		after := ansi.TruncateLeft(l, end, "")
		if ansi.StringWidth(after) > rest { // kept the one under the box's edge
			after = ansi.TruncateLeft(l, end+1, "")
		}
		after = strings.Repeat(" ", max(0, rest-ansi.StringWidth(after))) + after
		// Reset first so the cells under the box don't lend it their style.
		lines[top+i] = before + "\x1b[m" + row + "\x1b[m" + after
	}
	return strings.Join(lines, "\n")
}

// overlayPicker centers the picker over main.
func (m *Model) overlayPicker(main string) string {
	if m.modal == nil {
		return main
	}
	width := min(120, max(24, m.chatWidth()*9/10))
	box := m.renderPicker(width)
	top := max(0, (lipgloss.Height(main)-lipgloss.Height(box))/2)
	left := max(0, (lipgloss.Width(main)-lipgloss.Width(box))/2)
	return placeOver(main, box, top, left)
}
