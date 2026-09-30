// Package tui is the Bubble Tea frontend: the agent loop runs in a
// goroutine and its callbacks come back to the UI as messages.
//   - bridge.go: the messages a turn produces and RunTurn, which runs one
//   - model.go: Model, its state and Update
//   - view.go: layout and View
//   - transcript.go: the conversation as the UI shows it
//   - style.go: palette and shared styles
//   - sanitize.go: keeping model and tool text from driving the terminal
//   - input.go, input_unix.go, input_other.go: terminal input that keeps
//     escape sequences whole
package tui

import (
	"context"
	"encoding/json"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/tool"
)

// StreamMsg is one streamed model event forwarded from core.Loop.
type StreamMsg model.Event

// ToolResultMsg is one dispatched tool call's outcome.
type ToolResultMsg struct {
	Call   model.ToolCall
	Result tool.Result
	Err    error
}

// TurnDoneMsg marks the end of a Loop.Run call.
type TurnDoneMsg struct {
	Answer string
	Err    error
}

// CompactMsg is a compaction's start or end forwarded from core.Loop.
type CompactMsg core.CompactEvent

// NoticeMsg is a line for the transcript from outside the turn's stream.
type NoticeMsg string

// RunTurn runs loop.Run in a goroutine, forwarding its callbacks and
// completion through send as tea.Msgs. Callers must not start another turn
// on the same loop before TurnDoneMsg arrives. The returned channel closes
// when the goroutine exits.
func RunTurn(ctx context.Context, loop *core.Loop, input string, send func(tea.Msg)) <-chan struct{} {
	loop.OnEvent = func(e model.Event) {
		send(StreamMsg(e))
	}
	loop.OnToolResult = func(call model.ToolCall, res tool.Result, err error) {
		send(ToolResultMsg{Call: call, Result: res, Err: err})
	}
	loop.OnCompact = func(e core.CompactEvent) {
		send(CompactMsg(e))
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverTurn(send)
		answer, err := loop.Run(ctx, input)
		send(TurnDoneMsg{Answer: answer, Err: err})
	}()
	return done
}

// recoverTurn ends a turn that panicked as a failed one, so the UI keeps
// running and restores the terminal on exit.
func recoverTurn(send func(tea.Msg)) {
	if p := recover(); p != nil {
		send(TurnDoneMsg{Err: fmt.Errorf("internal error: %v", p)})
	}
}

// DenyPrompter denies every call that needs approval and says so in the
// transcript. The terminal prompter can't read stdin while Bubble Tea owns
// it, and the UI has no approval prompt of its own yet.
type DenyPrompter struct {
	Send func(tea.Msg)
}

// Prompt denies, telling the user why.
func (p DenyPrompter) Prompt(name string, _ json.RawMessage) permission.Decision {
	p.Send(NoticeMsg(fmt.Sprintf("Denied %s: the TUI can't ask for approval yet. Run wisp with a prompt argument to be asked in the terminal.", name)))
	return permission.Deny
}
