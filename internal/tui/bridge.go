// Package tui is the Bubble Tea frontend: the agent loop runs in a
// goroutine and its callbacks come back to the UI as messages.
//   - bridge.go: the messages a turn produces and RunTurn, which runs one
//   - model.go: Model, its state and Update
//   - view.go: layout and View
//   - keys.go: key handling: cancelling and quitting, scrolling, input
//     history, selecting blocks
//   - command.go: slash commands and the help overlay
//   - command_popup.go: the popup completing commands and their arguments
//   - picker.go: the modal list /model and /resume choose from
//   - model_switch.go: listing, switching, and describing models
//   - session.go: resuming and starting sessions mid-run
//   - panels.go: the side column: tasks and debug, and the task list
//   - debug_panel.go: token use, context budget, cost, and speed
//   - context.go: /context, the window drawn as a grid by category
//   - compact.go: /compact, and compactions shown in the transcript
//   - suggest.go: a suggested next message after each reply (--suggest)
//   - agents.go: sub-agent runs: the Agents panel and their tool cards
//   - agentview.go: a sub-agent's own conversation, in place of the chat
//   - logo.go: the splash, and uploading images to kitty-graphics terminals
//   - mascot.go: the animated mascot in the corner, and its easter eggs
//   - termcolors.go, termcolors_unix.go, termcolors_other.go: asking the
//     terminal for its colors, to draw the mascot in them
//   - mouse.go: hover, clicks, drag selection, and copying (OSC 52 as a
//     fallback)
//   - permission.go: Prompter, which asks for approval through the UI
//   - approval.go: what an approval shows: the call, a diff for edits
//   - blocks.go: the transcript as blocks, built from the stream and
//     replayed history, and expanding them
//   - render.go: drawing each kind of block: prompts, answers, reasoning,
//     tool cards
//   - markdown.go: answers as Markdown, with highlighted code blocks
//   - toolview.go: an expanded tool call's output, laid out per tool
//   - style.go: palette and shared styles
//   - sanitize.go: keeping model and tool text from driving the terminal
//   - input.go, input_unix.go, input_other.go: terminal input that keeps
//     escape sequences whole
package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
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

// TurnDoneMsg marks the end of a Loop.Run call, or of a /compact.
type TurnDoneMsg struct {
	Answer  string
	Err     error
	Compact bool // it ended a /compact, not a turn
}

// CompactMsg is a compaction's start or end forwarded from core.Loop.
type CompactMsg core.CompactEvent

// UpdateMsg is the version of a newer wisp release, found after startup.
type UpdateMsg string

// StatsMsg is one step's stats snapshot forwarded from core.Loop.
type StatsMsg core.StepStats

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
	loop.OnStats = func(s core.StepStats) {
		send(StatsMsg(s))
	}
	loop.OnCompact = func(e core.CompactEvent) {
		send(CompactMsg(e))
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverTurn(send, false)
		answer, err := loop.Run(ctx, input)
		send(TurnDoneMsg{Answer: answer, Err: err})
	}()
	return done
}

// recoverTurn ends a turn that panicked as a failed one, so the UI keeps
// running and restores the terminal on exit.
func recoverTurn(send func(tea.Msg), compact bool) {
	if p := recover(); p != nil {
		send(TurnDoneMsg{Err: fmt.Errorf("internal error: %v", p), Compact: compact})
	}
}

// RunCompact runs loop.Compact the way RunTurn runs a turn.
func RunCompact(ctx context.Context, loop *core.Loop, focus string, send func(tea.Msg)) <-chan struct{} {
	loop.OnCompact = func(e core.CompactEvent) {
		send(CompactMsg(e))
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverTurn(send, true)
		send(TurnDoneMsg{Err: loop.Compact(ctx, focus), Compact: true})
	}()
	return done
}
