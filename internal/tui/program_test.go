package tui

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

// These tests run the real bubbletea program (Init, the message pump from
// the turn goroutine, ticks, frame throttling, rendering) against a
// virtual terminal, and drive it with keys the way a user would.

// countingTool is a risky tool that counts its runs.
type countingTool struct{ runs *atomic.Int32 }

func (countingTool) Schema() model.ToolSchema { return model.ToolSchema{Name: "touch"} }
func (countingTool) Risky() bool              { return true }
func (t countingTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	t.runs.Add(1)
	return tool.Result{Content: "touched"}, nil
}

// startProgram runs a Model over provider in a 100×30 virtual terminal.
// Risky tools ask for approval through the TUI, as in the real program.
func startProgram(t *testing.T, provider model.Provider, opts Options, tools ...tool.Tool) *teatest.TestModel {
	t.Helper()
	ch := make(chan tea.Msg, 64)
	send := func(m tea.Msg) { ch <- m }
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	prompter := NewPrompter(send, done)
	var gated []tool.Tool
	for _, tl := range tools {
		gated = append(gated, permission.Gate{Tool: tl, Prompter: prompter})
	}
	loop := &core.Loop{Provider: provider, Tools: tool.NewRegistry(gated...), SessionID: "test-session"}
	return teatest.NewTestModel(t, NewModel(context.Background(), loop, send, ch, opts), teatest.WithInitialTermSize(100, 30))
}

// waitScreen waits until newly rendered output contains want.
func waitScreen(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(stripANSI(string(b)), want)
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
}

// quit exits the way a user does: Ctrl+C twice on an empty input.
func quit(t *testing.T, tm *teatest.TestModel) *Model {
	t.Helper()
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	return tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(*Model)
}

func TestProgramChatTurn(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{
		{Kind: model.EventReasoningDelta, Reasoning: "thinking it over"},
		{Kind: model.EventTextDelta, Text: "Hello from "},
		{Kind: model.EventTextDelta, Text: "the fake model"},
		{Kind: model.EventDone},
	}}}
	tm := startProgram(t, p, testOptions)
	waitScreen(t, tm, "ask wisp")

	tm.Type("hi there")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitScreen(t, tm, "Hello from the fake model")

	m := quit(t, tm)
	kinds := []blockKind{}
	for _, b := range m.blocks {
		kinds = append(kinds, b.kind)
	}
	if len(kinds) != 3 || kinds[0] != blockUser || kinds[1] != blockReasoning || kinds[2] != blockAnswer || m.inTurn {
		t.Fatalf("blocks %v, in turn %v", kinds, m.inTurn)
	}
}

func TestProgramApproval(t *testing.T) {
	var runs atomic.Int32
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c1", Name: "touch", Args: json.RawMessage(`{}`)}}, {Kind: model.EventDone}},
		{{Kind: model.EventTextDelta, Text: "all done"}, {Kind: model.EventDone}},
	}}
	tm := startProgram(t, p, testOptions, countingTool{&runs})
	waitScreen(t, tm, "ask wisp")
	tm.Type("touch it")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitScreen(t, tm, "Permission required")
	if runs.Load() != 0 {
		t.Fatal("the risky tool ran before approval")
	}

	time.Sleep(approvalGuard + 50*time.Millisecond) // a deliberate answer, not stray typing
	tm.Type("y")
	waitScreen(t, tm, "all done")
	quit(t, tm)
	if runs.Load() != 1 {
		t.Fatalf("tool ran %d times, want once", runs.Load())
	}
}

func TestProgramStartupModelPicker(t *testing.T) {
	p := &catalogProvider{models: []model.Info{
		{ID: "small", ContextWindow: 8192},
		{ID: "big", ContextWindow: 131072, Tools: model.Supported},
	}}
	opts := testOptions
	opts.Model, opts.Models = model.Info{}, p.models
	tm := startProgram(t, p, opts)
	waitScreen(t, tm, "Choose a model for this session")

	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitScreen(t, tm, "Using model") // the id is rendered as code, padded
	if m := quit(t, tm); m.opts.Model.ID != "big" || p.current != "big" {
		t.Fatalf("model %q, provider %q", m.opts.Model.ID, p.current)
	}
}

func TestProgramResize(t *testing.T) {
	tm := startProgram(t, &testutil.ScriptedProvider{}, testOptions)
	waitScreen(t, tm, "ask wisp")
	tm.Send(tea.WindowSizeMsg{Width: 15, Height: 5})
	waitScreen(t, tm, "Terminal too") // the notice itself is cut to 15 columns
	tm.Send(tea.WindowSizeMsg{Width: 60, Height: 20})
	waitScreen(t, tm, "ask wisp")
	quit(t, tm)
}
