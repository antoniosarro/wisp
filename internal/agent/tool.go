package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/tool"
)

// ToolName is the tool the main agent calls to start a sub-agent.
const ToolName = "agent"

// Options configures the agent tool.
type Options struct {
	Specs       []Spec
	Tools       *tool.Registry // what sub-agents may use; must not contain the agent tool
	NewProvider func(Spec) model.Provider
	// Info, if set, describes a spec's model at the start of a run: its
	// context window and output cap, so the run masks and compacts like
	// the main agent, and its list price, for requests not billed.
	Info        func(Spec) model.Info
	System      func(Spec) string // builds a sub-agent's system prompt
	MaxParallel int
	Observer    func(Event) // optional; called from the run's goroutine
	Trace       func(Trace) // optional; every streamed event and tool result
}

// Tool starts sub-agents on behalf of the main agent.
type Tool struct {
	opts      Options
	specs     map[string]Spec
	providers map[string]model.Provider
	registry  map[string]*tool.Registry // each agent's tools
	slots     chan struct{}             // one per run allowed at once
	nextRun   atomic.Int64
}

// NewTool validates specs against the available tools and builds each
// agent's provider and tool set.
func NewTool(opts Options) (*Tool, error) {
	t := &Tool{
		opts:      opts,
		specs:     map[string]Spec{},
		providers: map[string]model.Provider{},
		registry:  map[string]*tool.Registry{},
		slots:     make(chan struct{}, max(1, opts.MaxParallel)),
	}
	for _, s := range opts.Specs {
		names := s.Tools
		if len(names) == 0 {
			for _, schema := range opts.Tools.Schemas() {
				names = append(names, schema.Name)
			}
		}
		var tools []tool.Tool
		for _, name := range names {
			tl, ok := opts.Tools.Get(name)
			if !ok {
				return nil, fmt.Errorf("agent %s (%s): unknown tool %q", s.Name, s.Path, name)
			}
			tools = append(tools, tl)
		}
		t.specs[s.Name] = s
		t.registry[s.Name] = tool.NewRegistry(tools...)
		t.providers[s.Name] = opts.NewProvider(s)
	}
	return t, nil
}

// Schema lists the agents, with the descriptions that tell the main agent
// when to delegate to each.
func (t *Tool) Schema() model.ToolSchema {
	var list strings.Builder
	names := make([]string, 0, len(t.opts.Specs))
	for _, s := range t.opts.Specs {
		fmt.Fprintf(&list, "\n- %s: %s", s.Name, strings.TrimSpace(s.Description))
		names = append(names, s.Name)
	}
	enum, _ := json.Marshal(names)
	return model.ToolSchema{
		Name: ToolName,
		Description: "Delegate a self-contained task to a sub-agent. It works on its own with its own tools and returns only a final report, " +
			"which keeps your context small. It cannot see this conversation, so describe the task completely. Available agents:" + list.String(),
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"agent": {"type": "string", "enum": ` + string(enum) + `, "description": "which sub-agent to run"},
				"task": {"type": "string", "description": "complete description of the task and what to report back"}
			},
			"required": ["agent", "task"]
		}`),
	}
}

// Risky is false: the sub-agent's own risky calls ask for approval.
func (t *Tool) Risky() bool { return false }

// Run starts the agent's loop on the task, once one of MaxParallel slots is
// free, and returns its final message as the result. Its tool calls carry
// its name (permission.WithOrigin), so approval prompts say who asks. A
// failed run is an error result the main agent can react to; a cancelled
// one is the turn's error.
func (t *Tool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var a struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.Result{}, fmt.Errorf("decoding args: %w", err)
	}
	spec, ok := t.specs[a.Agent]
	if !ok {
		return tool.Result{}, fmt.Errorf("unknown agent %q", a.Agent)
	}
	if strings.TrimSpace(a.Task) == "" {
		return tool.Result{}, fmt.Errorf("task is required")
	}

	r := &run{observer: t.opts.Observer, trace: t.opts.Trace, ev: Event{
		RunID:  t.nextRun.Add(1),
		CallID: tool.CallID(ctx),
		Agent:  spec.Name,
		Task:   a.Task,
	}}
	r.emit()
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		r.finish("", ctx.Err())
		return tool.Result{}, ctx.Err()
	}

	r.update(func(e *Event) { e.Status, e.Started, e.Activity = Running, time.Now(), "starting" })
	loop := &core.Loop{
		Provider:      t.providers[spec.Name],
		Tools:         t.registry[spec.Name],
		System:        t.opts.System(spec),
		MaxIterations: spec.MaxIterations,
		OnEvent:       r.onEvent,
		OnToolResult:  r.onToolResult,
		OnStats:       r.onStats,
	}
	if t.opts.Info != nil {
		info := t.opts.Info(spec)
		loop.ContextWindow, loop.MaxOutput, loop.Price = info.ContextWindow, info.MaxOutput, info.Price
		loop.AutoCompact = info.ContextWindow > 0 // summaries go to the sub-agent's own model
		loop.NoTools = info.Tools == model.Unsupported
	}
	report, err := loop.Run(permission.WithOrigin(ctx, spec.Name), a.Task)
	r.finish(report, err)
	switch {
	case ctx.Err() != nil:
		return tool.Result{}, ctx.Err()
	case err != nil:
		return tool.Result{Content: fmt.Sprintf("sub-agent %s failed: %v", spec.Name, err), IsError: true}, nil
	case strings.TrimSpace(report) == "":
		return tool.Result{Content: fmt.Sprintf("sub-agent %s finished without a report", spec.Name)}, nil
	}
	return tool.Result{Content: report}, nil
}
