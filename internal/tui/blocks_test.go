package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

func TestBlocksStreamIntoOneBlock(t *testing.T) {
	var l blockList
	l.appendEvent(model.Event{Kind: model.EventReasoningDelta, Reasoning: "think"})
	l.appendEvent(model.Event{Kind: model.EventReasoningDelta, Reasoning: "ing"})
	l.appendEvent(model.Event{Kind: model.EventTextDelta, Text: "hel"})
	l.appendEvent(model.Event{Kind: model.EventTextDelta, Text: "lo"})

	if len(l) != 2 || l[0].reasoningText != "thinking" || !l[0].reasoningDone || l[1].kind != blockAnswer || l[1].text != "hello" {
		t.Errorf("blocks = %+v, want finished reasoning then the answer", l)
	}
}

func TestBlocksReclassifyAnswerAsReasoning(t *testing.T) {
	var l blockList
	l.appendEvent(model.Event{Kind: model.EventTextDelta, Text: "was thinking"})
	l.appendEvent(model.Event{Kind: model.EventReclassify})
	l.appendEvent(model.Event{Kind: model.EventTextDelta, Text: "answer"})

	if len(l) != 2 || l[0].kind != blockReasoning || l[0].reasoningText != "was thinking" || l[1].text != "answer" {
		t.Errorf("blocks = %+v, want the first text moved to reasoning", l)
	}
}

// The original matched the most recent block with the call's ID, finished
// or not: two parallel calls without IDs both resolved the second block,
// and the first was reported interrupted.
func TestBlocksResolveCallsInOrder(t *testing.T) {
	var l blockList
	for _, name := range []string{"read", "grep"} {
		l.appendEvent(model.Event{Kind: model.EventToolCall, ToolCall: &model.ToolCall{Name: name, Args: json.RawMessage(`{}`)}})
	}
	l.resolve(model.ToolCall{Name: "read"}, tool.Result{Content: "file"}, nil)
	l.resolve(model.ToolCall{Name: "grep"}, tool.Result{}, errors.New("bad pattern"))

	if l[0].toolStatus != toolOK || l[0].toolResult != "file" || l[1].toolStatus != toolFailed || l[1].toolResult != "bad pattern" {
		t.Errorf("blocks = %+v, want read ok and grep failed", l)
	}
}

func TestBlocksResolveDenial(t *testing.T) {
	var l blockList
	l.appendEvent(model.Event{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "1", Name: "bash"}})
	l.resolve(model.ToolCall{ID: "1"}, tool.Result{IsError: true, Content: permission.DeniedContent + ". Use ls instead"}, nil)

	if l[0].toolStatus != toolDenied || l[0].toolResult != "Use ls instead" {
		t.Errorf("block = %+v, want denied with the note", l[0])
	}
}

func TestBlocksSettle(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{fmt.Errorf("stream: %w", context.Canceled), "interrupted"},
		{errors.New("endpoint down"), "endpoint down"},
	} {
		var l blockList
		l.appendEvent(model.Event{Kind: model.EventReasoningDelta, Reasoning: "hm"})
		l.appendEvent(model.Event{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "1", Name: "bash"}})
		l.settle(tc.err)
		if !l[0].reasoningDone || l[1].toolStatus != toolFailed {
			t.Errorf("settle(%v) left %+v open", tc.err, l)
		}
		last := l[len(l)-1]
		if got := last.kind == blockTurnError; got != (tc.want != "") || got && last.text != tc.want {
			t.Errorf("settle(%v) ended with %+v, want error %q", tc.err, last, tc.want)
		}
	}
}

func TestReplayHistory(t *testing.T) {
	loop := &core.Loop{Provider: &testutil.ScriptedProvider{}, Tools: tool.NewRegistry(), History: []model.Message{
		{Role: model.RoleUser, Content: "list files"},
		// Providers that number calls per response reuse IDs; the first
		// call has no saved result and must not take the second's.
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call_0", Name: "ls", Args: json.RawMessage(`{}`)}}},
		{Role: model.RoleUser, Content: core.ReminderPrefix + "update your todos"},
		{Role: model.RoleAssistant, Content: core.InterruptedReply},
		{Role: model.RoleUser, Content: "try again"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call_0", Name: "ls", Args: json.RawMessage(`{}`)}}},
		{Role: model.RoleTool, ToolCallID: "call_0", Content: "a.go"},
		{Role: model.RoleAssistant, Content: "One file."},
	}}
	m := NewModel(context.Background(), loop, func(tea.Msg) {}, nil)

	var kinds []blockKind
	for _, b := range m.blocks {
		kinds = append(kinds, b.kind)
		if strings.HasPrefix(b.text, core.ReminderPrefix) || b.text == core.InterruptedReply {
			t.Errorf("replay shows %q, which the harness wrote", b.text)
		}
	}
	want := []blockKind{blockUser, blockToolCall, blockUser, blockToolCall, blockAnswer}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("replayed kinds = %v, want %v", kinds, want)
	}
	if first, second := m.blocks[1], m.blocks[3]; first.toolStatus != toolFailed || second.toolStatus != toolOK || second.toolResult != "a.go" {
		t.Errorf("replayed calls = %+v and %+v, want the first orphaned and the second resolved", first, second)
	}
}

// An escape sequence split across deltas is only recognizable once they
// are joined: sanitizing each delta on its own would print its tail.
func TestRenderSanitizesJoinedDeltas(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.blocks.appendEvent(model.Event{Kind: model.EventTextDelta, Text: "\x1b]0;pwn"})
	m.blocks.appendEvent(model.Event{Kind: model.EventTextDelta, Text: "ed\x07ok"})

	got := m.renderBlock(&m.blocks[0], 60, false)
	if strings.Contains(got, "\x1b]") || strings.TrimSpace(stripANSI(got)) != "● ok" {
		t.Errorf("render = %q, want only the answer \"ok\"", got)
	}
}

// The model names its tool calls, and a card shows the name before any
// tool has checked it: the original printed it raw.
func TestRenderSanitizesToolCall(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.blocks.appendEvent(model.Event{Kind: model.EventToolCall, ToolCall: &model.ToolCall{
		Name: "bash\x1b]52;c;aGk=\x07\x1b[2J",
		Args: json.RawMessage(`{"command":"ls \u001b[31m"}`),
	}})
	for _, status := range []toolStatus{toolRunning, toolOK, toolDenied} {
		b := m.blocks[0]
		b.toolStatus, b.toolResult, b.expanded = status, "out\x1b[2Jput", true
		if got := m.renderBlock(&b, 60, false); strings.Contains(got, "\x1b]52") || strings.Contains(got, "\x1b[2J") || strings.Contains(got, "\x1b[31m") {
			t.Errorf("status %v: render kept an escape sequence from the model: %q", status, got)
		}
	}
}

func TestCtrlRTogglesReasoning(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.blocks.appendEvent(model.Event{Kind: model.EventReasoningDelta, Reasoning: "weighing the options"})
	m.blocks.settle(nil)
	m.syncViewport()
	if view := stripANSI(m.View()); !strings.Contains(view, "Thought for 3 words") || strings.Contains(view, "weighing") {
		t.Fatalf("collapsed reasoning:\n%s", view)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if view := stripANSI(m.View()); !strings.Contains(view, "weighing the options") {
		t.Errorf("after Ctrl+R the reasoning is hidden:\n%s", view)
	}
}
