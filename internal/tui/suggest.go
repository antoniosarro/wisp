package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
)

// suggestTimeout bounds a suggestion request.
const suggestTimeout = time.Minute

// suggestionMsg carries a proposed next message for request gen.
type suggestionMsg struct {
	gen  int
	text string
}

// requestSuggestion asks the model for a likely next message in the
// background, replacing any request still running.
func (m *Model) requestSuggestion() tea.Cmd {
	m.clearSuggestion()
	ctx, cancel := context.WithTimeout(m.ctx, suggestTimeout)
	m.suggestCancel = cancel
	gen := m.suggestGen
	provider, tools, history := m.loop.Provider, m.loop.Tools, m.loop.Messages()
	return func() tea.Msg {
		defer cancel()
		text, err := core.SuggestNext(ctx, provider, tools, history)
		if err != nil {
			return nil
		}
		return suggestionMsg{gen: gen, text: text}
	}
}

// showSuggestion shows the latest request's suggestion in the empty
// input, unless a turn started since.
func (m *Model) showSuggestion(msg suggestionMsg) {
	if msg.gen != m.suggestGen || m.inTurn || msg.text == "" {
		return
	}
	// The model wrote it, and the placeholder is drawn as it is.
	m.suggestion = sanitize(msg.text)
	m.input.Placeholder = m.suggestion + "  → to accept"
}

// acceptSuggestion fills an empty input with the suggestion.
func (m *Model) acceptSuggestion() bool {
	if m.suggestion == "" || m.input.Value() != "" {
		return false
	}
	m.input.SetValue(m.suggestion)
	m.clearSuggestion()
	m.applyLayout()
	return true
}

// clearSuggestion drops the shown suggestion and cancels a pending request.
func (m *Model) clearSuggestion() {
	if m.suggestCancel != nil {
		m.suggestCancel()
		m.suggestCancel = nil
	}
	m.suggestGen++
	m.suggestion = ""
	m.input.Placeholder = inputPlaceholder
}
