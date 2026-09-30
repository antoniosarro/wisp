package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

// collect drains msgs into a channel-fed send func, blocking until
// TurnDoneMsg arrives, then returns everything received in order.
func collect(t *testing.T, run func(send func(tea.Msg))) []tea.Msg {
	t.Helper()
	ch := make(chan tea.Msg, 64)
	run(func(m tea.Msg) { ch <- m })

	var got []tea.Msg
	for {
		select {
		case m := <-ch:
			got = append(got, m)
			if _, ok := m.(TurnDoneMsg); ok {
				return got
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for TurnDoneMsg")
		}
	}
}

func TestRunTurnForwardsStreamAndDone(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{
			{Kind: model.EventTextDelta, Text: "hel"},
			{Kind: model.EventTextDelta, Text: "lo"},
			{Kind: model.EventDone},
		},
	}}
	loop := &core.Loop{Provider: p, Tools: tool.NewRegistry()}

	got := collect(t, func(send func(tea.Msg)) {
		RunTurn(context.Background(), loop, "hi", send)
	})

	// text, text, the EventDone stream delta itself (forwarded like any
	// other event), the step's StatsMsg, then the loop's own TurnDoneMsg.
	if len(got) != 5 {
		t.Fatalf("got %d messages, want 5: %+v", len(got), got)
	}
	if m, ok := got[0].(StreamMsg); !ok || m.Kind != model.EventTextDelta || m.Text != "hel" {
		t.Errorf("msg 0 = %+v", got[0])
	}
	if m, ok := got[1].(StreamMsg); !ok || m.Kind != model.EventTextDelta || m.Text != "lo" {
		t.Errorf("msg 1 = %+v", got[1])
	}
	if m, ok := got[2].(StreamMsg); !ok || m.Kind != model.EventDone {
		t.Errorf("msg 2 = %+v, want a StreamMsg carrying EventDone", got[2])
	}
	if _, ok := got[3].(StatsMsg); !ok {
		t.Errorf("msg 3 = %+v, want a StatsMsg", got[3])
	}
	done, ok := got[4].(TurnDoneMsg)
	if !ok || done.Answer != "hello" || done.Err != nil {
		t.Errorf("msg 4 = %+v, want TurnDoneMsg{Answer: hello}", got[4])
	}
}

func TestRunTurnForwardsToolResult(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{
			{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "call_1", Name: "echo", Args: json.RawMessage(`{"x":1}`)}},
			{Kind: model.EventDone},
		},
		{
			{Kind: model.EventTextDelta, Text: "done"},
			{Kind: model.EventDone},
		},
	}}
	loop := &core.Loop{Provider: p, Tools: tool.NewRegistry(testutil.EchoTool{})}

	got := collect(t, func(send func(tea.Msg)) {
		RunTurn(context.Background(), loop, "run echo", send)
	})

	var sawToolResult bool
	for _, m := range got {
		if tr, ok := m.(ToolResultMsg); ok {
			sawToolResult = true
			if tr.Call.Name != "echo" || tr.Result.Content != `echoed: {"x":1}` || tr.Err != nil {
				t.Errorf("ToolResultMsg = %+v", tr)
			}
		}
	}
	if !sawToolResult {
		t.Fatal("no ToolResultMsg observed")
	}

	last := got[len(got)-1]
	done, ok := last.(TurnDoneMsg)
	if !ok || done.Answer != "done" {
		t.Errorf("last msg = %+v, want TurnDoneMsg{Answer: done}", last)
	}
}

func TestRunTurnForwardsError(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventError, Err: errors.New("boom")}},
	}}
	loop := &core.Loop{Provider: p, Tools: tool.NewRegistry()}

	got := collect(t, func(send func(tea.Msg)) {
		RunTurn(context.Background(), loop, "hi", send)
	})

	last := got[len(got)-1]
	done, ok := last.(TurnDoneMsg)
	if !ok || done.Err == nil {
		t.Errorf("last msg = %+v, want a TurnDoneMsg carrying an error", last)
	}
}

// panickingProvider fails the way a bug in the loop or a provider would.
type panickingProvider struct{}

func (panickingProvider) Stream(context.Context, model.Request) (<-chan model.Event, error) {
	panic("boom")
}

func TestRunTurnRecoversPanic(t *testing.T) {
	loop := &core.Loop{Provider: panickingProvider{}, Tools: tool.NewRegistry()}

	var done <-chan struct{}
	got := collect(t, func(send func(tea.Msg)) {
		done = RunTurn(context.Background(), loop, "hi", send)
	})

	last, ok := got[len(got)-1].(TurnDoneMsg)
	if !ok || last.Err == nil || !strings.Contains(last.Err.Error(), "internal error: boom") {
		t.Errorf("last msg = %+v, want a TurnDoneMsg carrying the panic", got[len(got)-1])
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunTurn's channel did not close after the panic")
	}
}
