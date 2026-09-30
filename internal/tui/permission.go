package tui

import (
	"context"
	"encoding/json"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/tool"
)

var (
	_ permission.Prompter     = (*Prompter)(nil)
	_ permission.NotePrompter = (*Prompter)(nil)
)

// Answer is the user's reply to a PermissionRequestMsg; Note, if set,
// tells the model what to do instead of a denied call.
type Answer struct {
	Decision permission.Decision
	Note     string
}

// PermissionRequestMsg asks the Model to approve a risky call. Reply is
// buffered so answering never blocks Update; the Model queues concurrent
// requests and shows one at a time.
type PermissionRequestMsg struct {
	Context context.Context
	Agent   string // sub-agent making the call, or "" for the main agent
	CallID  string // the tool call asking, when known
	Name    string
	Args    json.RawMessage
	Reply   chan<- Answer
}

// Prompter asks for approval through the TUI, since bubbletea owns stdin.
type Prompter struct {
	send func(tea.Msg)
	done <-chan struct{} // closed when the program is shutting down
}

// NewPrompter builds a Prompter; closing done denies any pending prompt.
func NewPrompter(send func(tea.Msg), done <-chan struct{}) *Prompter {
	return &Prompter{send: send, done: done}
}

// Prompt asks without a context to cancel the wait.
func (p *Prompter) Prompt(name string, args json.RawMessage) permission.Decision {
	return p.PromptContext(context.Background(), name, args)
}

// PromptContext asks, denying if ctx ends first.
func (p *Prompter) PromptContext(ctx context.Context, name string, args json.RawMessage) permission.Decision {
	d, _ := p.PromptNote(ctx, name, args)
	return d
}

// PromptNote asks, returning with a denial the user's note, if any. It
// denies without asking once ctx has ended, and when ctx ends or the
// program stops before an answer.
func (p *Prompter) PromptNote(ctx context.Context, name string, args json.RawMessage) (permission.Decision, string) {
	if ctx.Err() != nil {
		return permission.Deny, ""
	}
	reply := make(chan Answer, 1)
	p.send(PermissionRequestMsg{Context: ctx, Agent: permission.Origin(ctx), CallID: tool.CallID(ctx), Name: name, Args: args, Reply: reply})

	select {
	case a := <-reply:
		return a.Decision, a.Note
	case <-p.done:
		return permission.Deny, ""
	case <-ctx.Done():
		return permission.Deny, ""
	}
}
