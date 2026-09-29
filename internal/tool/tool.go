// Package tool defines the Tool contract, a registry, and a concurrent
// dispatcher for a turn's tool calls (dispatch.go). The built-in tools are
// in the builtin subpackage.
package tool

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
)

// Result is fed back to the model as a tool-role message.
type Result struct {
	Content string
	IsError bool // the tool itself reports failure (e.g. bash non-zero exit)
	Images  []model.Image
}

// Tool is implemented by built-in and plugin tools alike. Run returns an
// error when the call could not be carried out at all (bad arguments, a
// missing file, a cancelled turn); a failure the model should read, such as
// a command's non-zero exit, is a Result with IsError.
type Tool interface {
	Schema() model.ToolSchema
	Risky() bool // gates the call behind the permission layer
	Run(ctx context.Context, args json.RawMessage) (Result, error)
}

// CallRisk is implemented by tools whose riskiness depends on the call,
// such as a proxy to other tools, or read, which asks only for credential
// files. Dispatch uses it in place of Risky.
type CallRisk interface {
	RiskyCall(args json.RawMessage) bool
}

// IsRisky reports whether this call of t is risky: CallRisk decides when t
// implements it, Risky otherwise.
func IsRisky(t Tool, args json.RawMessage) bool {
	if c, ok := t.(CallRisk); ok {
		return c.RiskyCall(args)
	}
	return t.Risky()
}

// Registry looks tools up by name and lists their schemas for a Request.
// It is not changed after NewRegistry, so it is safe for concurrent use.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry registers tools under their schema names; a later tool with
// the same name replaces an earlier one.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		r.tools[t.Schema().Name] = t
	}
	return r
}

// Get looks up a tool by name; a nil Registry has no tools.
func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	t, ok := r.tools[name]
	return t, ok
}

// Schemas lists the tools' schemas sorted by name, so the request, and
// with it the provider's prompt cache, is the same from turn to turn.
func (r *Registry) Schemas() []model.ToolSchema {
	schemas := make([]model.ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		schemas = append(schemas, t.Schema())
	}
	slices.SortFunc(schemas, func(a, b model.ToolSchema) int { return strings.Compare(a.Name, b.Name) })
	return schemas
}

// ChildEnv is the environment for processes that tools start: wisp's own
// without its API key, which a command or MCP server has no use for and
// could leak.
func ChildEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "WISP_API_KEY=") })
}
