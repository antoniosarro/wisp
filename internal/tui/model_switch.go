package tui

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
	tea "github.com/charmbracelet/bubbletea"
)

// modelsMsg carries the result of an async /model listing.
type modelsMsg struct {
	models    []model.Info
	err       error
	selection string // /model argument, empty to just list
}

// modelInfoMsg carries a model's description from the endpoint. quiet
// marks a background refresh, reported only if something changed.
type modelInfoMsg struct {
	info  model.Info
	err   error
	quiet bool
}

// catalog is the provider's model catalog, saying so if it has none.
func (m *Model) catalog() (model.Catalog, bool) {
	c, ok := m.loop.Provider.(model.Catalog)
	if !ok {
		m.notify(msgNoModelSwitch)
	}
	return c, ok
}

// listModels fetches the model list off the Update goroutine.
func (m *Model) listModels(selection string) tea.Cmd {
	catalog, ok := m.catalog()
	if !ok {
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		models, err := catalog.Models(ctx)
		return modelsMsg{models: models, err: err, selection: selection}
	}
}

// describeModel asks the endpoint about id off the Update goroutine. A
// quiet refresh says nothing, even when the provider can't describe models.
func (m *Model) describeModel(id string, quiet bool) tea.Cmd {
	catalog, ok := m.loop.Provider.(model.Catalog)
	if !ok {
		if !quiet {
			m.notify(msgNoModelSwitch)
		}
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		info, err := catalog.Describe(ctx, id)
		info.ID = id
		return modelInfoMsg{info: info, err: err, quiet: quiet}
	}
}

// handleModels opens the listing, or switches to the model /model named.
func (m *Model) handleModels(msg modelsMsg) tea.Cmd {
	_, isNumber := strconv.Atoi(msg.selection)
	switch {
	case msg.err != nil && (msg.selection == "" || isNumber == nil):
		m.notify("Model listing failed: " + msg.err.Error() + "\n\nCurrent model: " + m.currentModel() + "\nSwitch by name with /model NAME")
	case msg.err != nil:
		// Endpoints without /models (e.g. llama-swap) can still switch by name.
		return m.switchModel(msg.selection)
	case msg.selection == "":
		m.showModels(msg.models, "")
	default:
		m.knownModels = msg.models
		if name, ok := pickModel(msg.models, msg.selection); ok {
			return m.switchModel(name)
		}
		m.notify("Model not found: " + msg.selection)
	}
	return nil
}

// currentModel names the model in use, or "none".
func (m *Model) currentModel() string {
	return cmp.Or(m.opts.Model.ID, "none")
}

// modelItems lists models for the picker and the popup.
func (m *Model) modelItems(models []model.Info) []pickerItem {
	items := make([]pickerItem, len(models))
	for i, info := range models {
		items[i] = pickerItem{value: info.ID, title: info.ID, detail: info.Summary(), current: info.ID == m.opts.Model.ID}
	}
	return items
}

// showModels opens the model picker; choosing one switches to it.
func (m *Model) showModels(models []model.Info, heading string) {
	if len(models) == 0 {
		m.notify("No models returned by the endpoint.")
		return
	}
	m.knownModels = models
	m.openPicker(&picker{title: cmp.Or(heading, "Switch model"), items: m.modelItems(models), pick: m.switchModel})
}

// pickModel resolves a 1-based index or exact name against models.
func pickModel(models []model.Info, selection string) (string, bool) {
	if i, err := strconv.Atoi(selection); err == nil && i > 0 && i <= len(models) {
		return models[i-1].ID, true
	}
	return selection, slices.ContainsFunc(models, func(m model.Info) bool { return m.ID == selection })
}

// switchModel makes name the model for the next request at once, then
// learns its context window and capabilities in the background.
func (m *Model) switchModel(name string) tea.Cmd {
	if m.inTurn { // the loop reads the model's settings while it runs
		m.notify(msgBusySwitchModel)
		return nil
	}
	catalog, ok := m.catalog()
	if !ok {
		return nil
	}
	catalog.SetModel(name)
	m.opts.Model = model.Info{ID: name}
	m.redescribed = false
	m.closeHelp()
	return m.describeModel(name, false)
}

// useModel applies a described model to the loop and the display.
func (m *Model) useModel(msg modelInfoMsg) {
	if msg.info.ID != m.opts.Model.ID {
		return // a newer switch superseded this description
	}
	if m.inTurn { // e.g. the quiet refresh after a turn, once the next began
		m.laterModel = &msg
		return
	}
	info, effort := msg.info, m.loop.Effort
	if m.opts.OnModel != nil {
		info = m.opts.OnModel(m.loop, info)
	}
	changed := info != m.opts.Model
	m.opts.Model = info
	if msg.quiet && !changed {
		return
	}
	text := "Using model `" + info.ID + "` · " + info.Summary()
	if msg.err != nil {
		text += "\n\nThe endpoint's details are incomplete: " + msg.err.Error()
	}
	if effort != m.loop.Effort {
		text += "\n\nThis model doesn't take reasoning effort `" + sanitize(effort) + "`, so it uses its default; /effort lists its levels."
	}
	if info.Tools == model.Unsupported {
		text += "\n\nThe endpoint says this model cannot call tools, so wisp sends none: it can answer, but not read files or run commands."
	}
	m.notify(text)
}
