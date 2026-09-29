package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/span"
	"github.com/antoniosarro/wisp/internal/tool"
)

// CallTool is mcp_call: it validates arguments against the target tool's
// schema and runs it behind its own permission gate.
type CallTool struct {
	m    *Manager
	gate func(*Tool) tool.Tool

	mu    sync.Mutex
	gated map[*Tool]tool.Tool // each target's gate, made once
}

// NewCallTool proxies calls to m's tools; gate wraps each target.
func NewCallTool(m *Manager, gate func(*Tool) tool.Tool) *CallTool {
	return &CallTool{m: m, gate: gate, gated: map[*Tool]tool.Tool{}}
}

// callArgs is mcp_call's arguments.
type callArgs struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Schema is mcp_call's own, fixed definition.
func (c *CallTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name: CallToolName,
		Description: "Calls an MCP tool. Get its parameters from " + SearchToolName + " first. " +
			"name is the tool's name as " + SearchToolName + " lists it; arguments is an object matching its parameters.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"name":{"type":"string","description":"the MCP tool, e.g. mcp__server__tool"},` +
			`"arguments":{"type":"object","description":"the tool's arguments"}},` +
			`"required":["name"]}`),
	}
}

// Risky reports the proxy as a whole; RiskyCall decides per call.
func (c *CallTool) Risky() bool { return true }

// RiskyCall lets calls to read-only tools run in parallel with others.
func (c *CallTool) RiskyCall(args json.RawMessage) bool {
	var in callArgs
	if json.Unmarshal(args, &in) != nil {
		return true
	}
	x := lookup(c.m.Tools(), in.Name)
	return x == nil || x.Risky()
}

// Run validates the arguments against the target's schema, answering the
// model with the error and the schema when they don't fit, and runs the
// target through its gate.
func (c *CallTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var in callArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Result{}, fmt.Errorf("decoding args: %w", err)
	}
	x := lookup(c.m.Tools(), in.Name)
	if x == nil {
		return tool.Result{Content: fmt.Sprintf("unknown MCP tool %q; the %s description lists them", in.Name, SearchToolName), IsError: true}, nil
	}
	sp := span.FromContext(ctx)
	sp.Set("wisp.mcp.tool", x.name)
	if len(in.Arguments) == 0 || string(in.Arguments) == "null" {
		in.Arguments = json.RawMessage(`{}`)
	}
	if err := validate(x, in.Arguments); err != nil {
		sp.Set("wisp.mcp.invalid_arguments", err.Error())
		params, _ := json.Marshal(x.tool.Load().InputSchema)
		return tool.Result{Content: fmt.Sprintf("invalid arguments for %s: %v\nParameters: %s", x.name, err, params), IsError: true}, nil
	}
	return c.gatedFor(x).Run(ctx, in.Arguments)
}

// gatedFor is x behind its gate.
func (c *CallTool) gatedFor(x *Tool) tool.Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	g, ok := c.gated[x]
	if !ok {
		g = c.gate(x)
		c.gated[x] = g
	}
	return g
}

// validate checks args against x's input schema. A schema it can't read is
// left to the server to enforce.
func validate(x *Tool, args json.RawMessage) error {
	raw, err := json.Marshal(x.tool.Load().InputSchema)
	if err != nil {
		return err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil // a schema we can't read: leave validation to the server
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil
	}
	var value any
	if err := json.Unmarshal(args, &value); err != nil {
		return err
	}
	return resolved.Validate(value)
}
