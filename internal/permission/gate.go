package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/span"
	"github.com/antoniosarro/wisp/internal/tool"
)

// Gate wraps a Tool, validating required args and prompting before Run if
// the call is risky. It is a tool.Tool itself, so the registry and the
// dispatcher see gated tools like any other.
type Gate struct {
	Tool     tool.Tool
	Prompter Prompter // nil means a TerminalPrompter
	Rules    *Rules   // shared session rules; nil disables "always allow"
}

// Schema is the wrapped tool's.
func (g Gate) Schema() model.ToolSchema { return g.Tool.Schema() }

// Risky is the wrapped tool's.
func (g Gate) Risky() bool { return g.Tool.Risky() }

// RiskyCall passes the wrapped tool's per-call risk on, e.g. to Dispatch.
func (g Gate) RiskyCall(args json.RawMessage) bool { return tool.IsRisky(g.Tool, args) }

// Run rejects a call missing required arguments before asking, asks when
// the call is risky, and runs the tool if it is allowed. A denial is an
// IsError result the model reads, with the user's note if there is one.
func (g Gate) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	schema := g.Tool.Schema()
	// Asking about a call that can't run would waste the user's attention.
	if err := checkRequired(schema.Parameters, args); err != nil {
		return tool.Result{}, err
	}
	if tool.IsRisky(g.Tool, args) {
		if ok, note := g.approved(ctx, schema.Name, args); !ok {
			if err := ctx.Err(); err != nil {
				return tool.Result{}, err // cancelled while asking, not denied
			}
			content := DeniedContent
			if note != "" {
				content += ". The user says: " + note
			}
			return tool.Result{Content: content, IsError: true}, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	return g.Tool.Run(ctx, args)
}

// decisionNames label decisions in traces.
var decisionNames = [...]string{Allow: "allow", AllowAlways: "always allow", Deny: "deny"}

// approved consults the session rules, then the user, returning any note
// that came with a denial. A wait for the user is traced as an approval
// span; an answer from the rules or AllowAll is only noted on the call's.
func (g Gate) approved(ctx context.Context, name string, args json.RawMessage) (bool, string) {
	key := RuleKey(name, args)
	if g.Rules != nil && g.Rules.allows(key) {
		span.FromContext(ctx).Set("wisp.approval", "rule: "+RuleLabel(name, args))
		return true, ""
	}
	prompter := g.Prompter
	if prompter == nil {
		prompter = TerminalPrompter{}
	}
	if _, ok := prompter.(AllowAll); ok {
		span.FromContext(ctx).Set("wisp.approval", "allowed without asking")
		return true, ""
	}
	ctx, sp := span.Start(ctx, span.KindApproval, name)
	var d Decision
	var note string
	switch p := prompter.(type) {
	case NotePrompter:
		d, note = p.PromptNote(ctx, name, args)
	case ContextPrompter:
		d = p.PromptContext(ctx, name, args)
	default:
		d = prompter.Prompt(name, args)
	}
	if d == AllowAlways && g.Rules != nil {
		g.Rules.add(key)
	}
	sp.Set("wisp.decision", decisionNames[d])
	if note != "" {
		sp.Set("wisp.note", note)
	}
	switch {
	case ctx.Err() != nil:
		sp.End(span.StatusCancelled)
	case d == Deny:
		sp.End(span.StatusDenied)
	default:
		sp.End(span.StatusOK)
	}
	return d != Deny, note
}

// checkRequired verifies args has a non-null value for each property the
// JSON schema lists as required. Models sometimes send null for a value
// they don't have; tools would read it as empty.
func checkRequired(params, args json.RawMessage) error {
	if len(params) == 0 {
		return nil
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(params, &schema); err != nil {
		return fmt.Errorf("reading the tool's schema: %w", err)
	}
	if len(schema.Required) == 0 {
		return nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(args, &values); err != nil {
		return fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	for _, name := range schema.Required {
		if value, ok := values[name]; !ok || strings.TrimSpace(string(value)) == "null" {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

// GateAll wraps every tool in a Gate sharing prompter and one session rule set.
func GateAll(prompter Prompter, tools ...tool.Tool) []tool.Tool {
	rules := &Rules{}
	gated := make([]tool.Tool, len(tools))
	for i, t := range tools {
		gated[i] = Gate{Tool: t, Prompter: prompter, Rules: rules}
	}
	return gated
}
