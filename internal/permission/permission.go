// Package permission gates risky tool calls behind user approval.
//
// A Gate wraps each tool (gate.go). When a call is risky, it checks the
// session's "always allow" rules (rules.go) and otherwise asks a Prompter:
// the TUI's, the terminal's (terminal.go), or AllowAll for
// --dangerously-skip-permissions.
package permission

import (
	"context"
	"encoding/json"
	"path/filepath"
)

// DeniedContent is the tool result content reported for a denied call.
const DeniedContent = "denied by user"

// Decision is the user's answer to an approval request.
type Decision int

const (
	Deny Decision = iota
	Allow
	AllowAlways // allow, and skip asking for matching calls this session
)

// originKey is the context key the calling sub-agent's name is stored under.
type originKey struct{}

// WithOrigin marks calls made under ctx as coming from the named sub-agent,
// so prompts can say who is asking.
func WithOrigin(ctx context.Context, agent string) context.Context {
	return context.WithValue(ctx, originKey{}, agent)
}

// Origin returns the sub-agent making calls under ctx, or "" for the main agent.
func Origin(ctx context.Context) string {
	name, _ := ctx.Value(originKey{}).(string)
	return name
}

// Prompter asks whether a risky call should run. The Gate uses the richest
// of these interfaces the prompter implements.
type Prompter interface {
	Prompt(name string, args json.RawMessage) Decision
}

// ContextPrompter supports cancellation while awaiting a decision: a
// cancelled turn must not leave a prompt waiting for an answer.
type ContextPrompter interface {
	PromptContext(context.Context, string, json.RawMessage) Decision
}

// NotePrompter is a ContextPrompter that can return, with a denial, the
// user's note on what to do instead, which is passed on to the model.
type NotePrompter interface {
	PromptNote(context.Context, string, json.RawMessage) (Decision, string)
}

// AllowAll approves every call (--dangerously-skip-permissions). The Gate
// recognizes it and records that nothing was asked.
type AllowAll struct{}

// Prompt allows.
func (AllowAll) Prompt(string, json.RawMessage) Decision { return Allow }

// LinkTarget returns the file a write to path lands in when that is another
// file, or "". The file tools follow symlinks, so a link in a project can
// aim a write at any file the user can write, such as ~/.bashrc: a prompt
// that named only the link would hide it.
func LinkTarget(path string) string {
	if path == "" {
		return ""
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "" // a new file, or a dangling link the tool would fail on
	}
	abs, err := filepath.Abs(path)
	if err != nil || abs == target {
		return ""
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil && filepath.Join(dir, filepath.Base(abs)) == target {
		return "" // only a directory on the way is a link: the file is the one named
	}
	return target
}

// writeTarget is LinkTarget for a call of a file-writing tool, or "" for
// any other call.
func writeTarget(name string, args json.RawMessage) string {
	switch name {
	case "write", "edit", "multi_edit":
	default:
		return ""
	}
	var a struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(args, &a)
	return LinkTarget(a.Path)
}
