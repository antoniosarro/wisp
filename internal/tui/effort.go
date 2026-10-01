package tui

import (
	"cmp"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	tea "github.com/charmbracelet/bubbletea"
)

// defaultEffort is the /effort choice that sends no level, leaving the
// effort to the model.
const defaultEffort = "default"

// efforts is the levels /effort takes for the current model: what the
// endpoint reports, else, on trust, every level wisp knows.
func (m *Model) efforts() model.Efforts {
	return cmp.Or(m.opts.Model.Efforts, model.AllEfforts)
}

// effortItems lists the levels for the picker and the popup: only those
// the endpoint reported, so a model not yet described offers just the
// default.
func (m *Model) effortItems() []pickerItem {
	items := []pickerItem{{value: defaultEffort, title: defaultEffort, detail: "the model's own", current: m.loop.Effort == ""}}
	for _, l := range m.opts.Model.Efforts.Levels() {
		it := pickerItem{value: l, title: l, current: l == m.loop.Effort}
		if l == "none" {
			it.detail = "no reasoning"
		}
		items = append(items, it)
	}
	return items
}

// pickEffort opens the effort picker; choosing a level sets it.
func (m *Model) pickEffort() {
	title := "Reasoning effort"
	switch {
	case m.opts.Model.Efforts != 0:
	case m.opts.Model.Local:
		title += " (levels are known once the model has loaded: send a message first)"
	default:
		title += " (the endpoint doesn't report this model's levels)"
	}
	m.openPicker(&picker{title: title, items: m.effortItems(), pick: m.setEffort})
}

// setEffort sends level, or the model's default, with the next requests.
func (m *Model) setEffort(level string) tea.Cmd {
	if m.inTurn { // the loop reads the effort while it runs
		m.notify(msgBusyEffort)
		return nil
	}
	m.closeOverlay()
	if level == defaultEffort {
		level = ""
	}
	if level != "" && !m.efforts().Has(level) {
		m.notify("`" + sanitize(m.currentModel()) + "` doesn't take reasoning effort `" + sanitize(level) + "`. Choose " +
			strings.Join(append([]string{defaultEffort}, m.efforts().Levels()...), ", ") + ".")
		return nil
	}
	m.loop.Effort = level
	m.notify("Reasoning effort: " + cmp.Or(level, "the model's default"))
	return nil
}
