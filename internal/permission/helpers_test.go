package permission

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// fakeTool records whether it ran; risky makes every call ask.
type fakeTool struct {
	risky bool
	ran   bool
}

func (f *fakeTool) Schema() model.ToolSchema { return model.ToolSchema{Name: "fake"} }
func (f *fakeTool) Risky() bool              { return f.risky }
func (f *fakeTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	f.ran = true
	return tool.Result{Content: "ran"}, nil
}

// bashLike is a risky tool named bash, so rules scope it like bash.
type bashLike struct{}

func (bashLike) Schema() model.ToolSchema { return model.ToolSchema{Name: "bash"} }
func (bashLike) Risky() bool              { return true }
func (bashLike) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{}, nil
}

// riskyReadTool is a read that asks only for .env paths, like the real
// read asks only for credentials.
type riskyReadTool struct{}

func (riskyReadTool) Schema() model.ToolSchema { return model.ToolSchema{Name: "read"} }
func (riskyReadTool) Risky() bool              { return false }
func (riskyReadTool) RiskyCall(args json.RawMessage) bool {
	return strings.Contains(string(args), ".env")
}
func (riskyReadTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

// spyPrompter answers with a fixed decision and records that it was asked.
type spyPrompter struct {
	allow  bool
	always bool
	called bool
}

func (s *spyPrompter) Prompt(string, json.RawMessage) Decision {
	s.called = true
	switch {
	case s.always:
		return AllowAlways
	case s.allow:
		return Allow
	}
	return Deny
}

// promptFunc adapts a function to Prompter.
type promptFunc func(string, json.RawMessage) Decision

func (f promptFunc) Prompt(name string, args json.RawMessage) Decision { return f(name, args) }

// notePrompter denies with a note.
type notePrompter struct{}

func (notePrompter) Prompt(string, json.RawMessage) Decision { return Deny }
func (notePrompter) PromptNote(context.Context, string, json.RawMessage) (Decision, string) {
	return Deny, "use pnpm"
}

// commandArgs is bash's arguments for cmd.
func commandArgs(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}
