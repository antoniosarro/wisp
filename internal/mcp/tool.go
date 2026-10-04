package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/span"
	"github.com/antoniosarro/wisp/internal/tool"
)

// Tool calls one tool on an MCP server. Its definition is replaced in
// place when the server's tool list changes.
type Tool struct {
	name   string
	server *server
	tool   atomic.Pointer[sdk.Tool]
	vision *atomic.Bool
}

// Schema is the server's definition, named mcp__<server>__<tool>. It is
// what tool_search returns, or, for a direct server, what the tool list
// carries.
func (t *Tool) Schema() model.ToolSchema {
	st := t.tool.Load()
	params, _ := json.Marshal(st.InputSchema)
	return model.ToolSchema{Name: t.name, Description: st.Description, Parameters: params}
}

// Risky unless the server declares the tool read-only, or its config does
// (ServerConfig.ReadOnly). Annotations are the server's word: only
// configure servers you trust.
func (t *Tool) Risky() bool {
	if t.server.readOnly {
		return false
	}
	a := t.tool.Load().Annotations
	return a == nil || !a.ReadOnlyHint
}

// Destructive reports whether the tool may destroy data, with the spec's
// defaults: read-only tools don't; others do unless they declare
// destructiveHint false.
func (t *Tool) Destructive() bool {
	a := t.tool.Load().Annotations
	if a == nil {
		return true
	}
	return !a.ReadOnlyHint && (a.DestructiveHint == nil || *a.DestructiveHint)
}

// Gate returns a function wrapping tools in permission gates that share one
// rule set. Destructive tools ask ask, so they still prompt when prompter
// allows everything (--dangerously-skip-permissions). mcp_call runs every
// tool through it.
func Gate(prompter, ask permission.Prompter) func(*Tool) tool.Tool {
	rules := &permission.Rules{}
	return func(t *Tool) tool.Tool {
		p := prompter
		if t.Destructive() {
			p = ask
		}
		return permission.Gate{Tool: t, Prompter: p, Rules: rules}
	}
}

// callTimeout bounds one MCP tool call.
const callTimeout = 10 * time.Minute

// Run calls the tool on its server, traced as an mcp span, and converts
// the result: text as text, images as images for a vision model,
// structured content as JSON when there is no text, and the rest described
// rather than fetched. Output is clipped like bash's.
func (t *Tool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	params := &sdk.CallToolParams{Name: t.tool.Load().Name}
	if len(args) > 0 && string(args) != "null" {
		params.Arguments = args
	}
	// A server that never answers would otherwise hold the turn until the
	// user cancels it.
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	ctx, sp := span.Start(ctx, span.KindMCP, t.server.name+"/"+params.Name)
	sp.Set("wisp.mcp.server", t.server.name)
	sp.Set("wisp.mcp.tool", params.Name)
	res, err := t.server.session.CallTool(ctx, params)
	if err != nil {
		sp.EndErr(ctx, err)
		return tool.Result{}, err
	}
	if res.IsError {
		sp.End(span.StatusError)
	} else {
		sp.End(span.StatusOK)
	}
	var out tool.Result
	var text []string
	for _, c := range res.Content {
		switch c := c.(type) {
		case *sdk.TextContent:
			text = append(text, c.Text)
		case *sdk.ImageContent:
			if t.vision != nil && t.vision.Load() {
				out.Images = append(out.Images, model.Image{MIME: c.MIMEType, Data: c.Data})
			} else {
				text = append(text, fmt.Sprintf("[image %s, %d bytes, not shown: the model has no vision]", c.MIMEType, len(c.Data)))
			}
		case *sdk.AudioContent:
			text = append(text, fmt.Sprintf("[audio %s, %d bytes, not shown]", c.MIMEType, len(c.Data)))
		case *sdk.ResourceLink:
			text = append(text, strings.TrimSpace(fmt.Sprintf("[resource %s] %s %s", c.URI, c.Name, c.Description)))
		case *sdk.EmbeddedResource:
			if r := c.Resource; r != nil && r.Text != "" {
				text = append(text, r.Text)
			} else if r != nil {
				text = append(text, fmt.Sprintf("[resource %s, %s, %d bytes, not shown]", r.URI, r.MIMEType, len(r.Blob)))
			}
		}
	}
	if len(text) == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			text = append(text, string(b))
		}
	}
	out.Content = tool.Clip(strings.Join(text, "\n"), tool.MaxOutputBytes, 0)
	out.IsError = res.IsError
	return out, nil
}
