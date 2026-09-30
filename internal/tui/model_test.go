package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

// newTestModel is a Model on a loop around p, sized to 80×24, with the
// channel its turns send to.
func newTestModel(t *testing.T, p model.Provider) (*Model, chan tea.Msg) {
	t.Helper()
	ch := make(chan tea.Msg, 64)
	loop := &core.Loop{Provider: p, Tools: tool.NewRegistry(testutil.EchoTool{})}
	m := NewModel(context.Background(), loop, func(msg tea.Msg) { ch <- msg }, ch)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m, ch
}

// typeText sends s as typed keys, then Enter.
func typeText(m *Model, s string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

// pump feeds the turn's messages to m until the turn ends.
func pump(t *testing.T, m *Model, ch chan tea.Msg) {
	t.Helper()
	for {
		select {
		case msg := <-ch:
			m.Update(msg)
			if _, ok := msg.(TurnDoneMsg); ok {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for TurnDoneMsg")
		}
	}
}

func TestModelRunsATurn(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{
			{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c1", Name: "echo", Args: json.RawMessage(`{"x":1}`)}},
			{Kind: model.EventDone},
		},
		{
			{Kind: model.EventTextDelta, Text: "all done"},
			{Kind: model.EventDone},
		},
	}}
	m, ch := newTestModel(t, p)

	typeText(m, "run echo")
	if !m.inTurn || m.input.Value() != "" {
		t.Fatalf("after Enter: inTurn = %v, input = %q; want a turn and a cleared input", m.inTurn, m.input.Value())
	}
	pump(t, m, ch)
	m.Wait()

	if m.inTurn {
		t.Error("the turn is still running after TurnDoneMsg")
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"▌ run echo", `✓ echo "x":1`, "● all done"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
}

func TestModelKeepsDraftDuringTurn(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.inTurn = true

	typeText(m, "next question")
	if m.input.Value() != "next question" || !strings.Contains(m.notice, "Still working") {
		t.Errorf("input = %q, notice = %q; want the draft kept and a notice", m.input.Value(), m.notice)
	}
}

// blockingProvider streams nothing until its request is cancelled.
type blockingProvider struct{}

func (blockingProvider) Stream(ctx context.Context, _ model.Request) (<-chan model.Event, error) {
	ch := make(chan model.Event, 1)
	go func() {
		<-ctx.Done()
		ch <- model.Event{Kind: model.EventError, Err: ctx.Err()}
		close(ch)
	}()
	return ch, nil
}

func TestModelEscInterruptsTurn(t *testing.T) {
	m, ch := newTestModel(t, blockingProvider{})

	typeText(m, "hi")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	pump(t, m, ch)

	if m.inTurn || !strings.Contains(ansi.Strip(m.View()), "✗ interrupted") {
		t.Errorf("after Esc: inTurn = %v, view:\n%s", m.inTurn, ansi.Strip(m.View()))
	}
}

// Every message from the channel must re-issue the listener, or the UI
// stops hearing from the turn.
func TestModelKeepsListening(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	for _, msg := range []tea.Msg{
		StreamMsg{Kind: model.EventTextDelta, Text: "x"},
		ToolResultMsg{},
		CompactMsg{},
		NoticeMsg("note"),
		TurnDoneMsg{},
	} {
		if _, cmd := m.Update(msg); cmd == nil {
			t.Errorf("Update(%T) returned no command, want the listener re-issued", msg)
		}
	}
}

func TestModelRefusesHugePaste(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune(strings.Repeat("x", maxPasteBytes+1))})

	if m.input.Value() != "" || !strings.Contains(m.notice, "not inserted") {
		t.Errorf("input has %d bytes, notice = %q; want the paste refused", len(m.input.Value()), m.notice)
	}
}

func TestViewTooSmall(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 19, Height: 5})

	if v := m.View(); !strings.Contains(v, "Terminal too small") {
		t.Errorf("View = %q, want the resize hint", v)
	}
}

func TestViewFitsTerminal(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.blocks = append(m.blocks,
		block{kind: blockUser, text: strings.Repeat("word ", 50)},
		block{kind: blockAnswer, text: strings.Repeat("a long answer line ", 200) + "\n\n```go\n" + strings.Repeat("x", 300) + "\n```"},
		toolCallBlock(model.ToolCall{Name: "write", Args: json.RawMessage(`{"content":"` + strings.Repeat("y", 300) + `"}`)}),
	)
	m.syncViewport()

	lines := strings.Split(m.View(), "\n")
	if len(lines) > 24 {
		t.Errorf("View is %d lines, want at most 24", len(lines))
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 80 {
			t.Errorf("line %q is %d columns, want at most 80", ansi.Strip(l), w)
		}
	}
}
