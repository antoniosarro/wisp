package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

func TestSuggestionAcceptedWithRightArrow(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "run the tests"}, {Kind: model.EventDone}}}}
	m, _ := newTestModel(t, p)
	m.opts.Suggest = true
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.Update(TurnDoneMsg{})
	if m.suggestCancel == nil {
		t.Fatal("no suggestion requested after the turn")
	}
	msg, ok := m.requestSuggestion()().(suggestionMsg)
	if !ok {
		t.Fatal("suggestion request returned no suggestion")
	}
	m.Update(msg)
	if m.suggestion != "run the tests" || m.input.Placeholder == inputPlaceholder {
		t.Fatalf("suggestion = %q, placeholder = %q", m.suggestion, m.input.Placeholder)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.input.Value() != "run the tests" || m.suggestion != "" || m.input.Placeholder != inputPlaceholder {
		t.Fatalf("after →: input = %q, suggestion = %q", m.input.Value(), m.suggestion)
	}
}

func TestStaleSuggestionIgnored(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.suggestGen = 3
	m.Update(suggestionMsg{gen: 2, text: "old"})
	if m.suggestion != "" {
		t.Fatalf("stale suggestion %q was shown", m.suggestion)
	}
}
