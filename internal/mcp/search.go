package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/textfmt"
	"github.com/antoniosarro/wisp/internal/tool"
)

// Limits of the index in tool_search's description, which every request
// carries.
const (
	maxIndexDescription  = 100 // characters of each tool's description in the index
	maxIndexInstructions = 500 // characters of each server's instructions
	defaultSearchResults = 5
)

// Names the model calls the two tools by.
const (
	SearchToolName = "tool_search"
	CallToolName   = "mcp_call"
)

// SearchTool is tool_search: it returns MCP tools' definitions as its
// result, so they enter the conversation rather than the tool list, which
// stays the same all session and keeps the prompt cache valid.
type SearchTool struct {
	m *Manager
}

// NewSearchTool searches m's tools.
func NewSearchTool(m *Manager) *SearchTool { return &SearchTool{m: m} }

// Prompt is a system prompt section on the connected servers. It names
// those behind tool_search, since small models overlook an index that
// lives only in a tool description, and carries the instructions of the
// direct ones, whose tools are in the tool list and not in the index.
func Prompt(m *Manager) string {
	var b strings.Builder
	b.WriteString("# MCP servers")
	var deferred []string
	for _, s := range m.servers {
		if !s.direct {
			deferred = append(deferred, s.name)
		}
	}
	if len(deferred) > 0 {
		b.WriteString("\nConnected: " + strings.Join(deferred, ", ") + ". " +
			"Their tools are not in your tool list. When a task involves one of these services, " +
			"call " + SearchToolName + " to get the tools' parameters (its description lists every tool), " +
			"then call them through " + CallToolName + ".")
	}
	for _, s := range m.servers {
		if !s.direct {
			continue
		}
		fmt.Fprintf(&b, "\n\n## %s\nIts tools are in your tool list, named %s<tool>.", s.name, toolName(s.name, ""))
		if s.instructions != "" {
			b.WriteString("\n")
			b.WriteString(textfmt.CutRunes(strings.TrimSpace(s.instructions), maxIndexInstructions))
		}
	}
	return b.String()
}

// Schema carries the index: every tool's name and the first line of its
// description, under each server's instructions, capped. Direct servers'
// tools are in the tool list instead.
func (t *SearchTool) Schema() model.ToolSchema {
	var b strings.Builder
	b.WriteString("Returns the parameters of MCP tools, which you then call through " + CallToolName + ". " +
		`Query "select:name1,name2" for exact names; other queries match keywords against names and descriptions. ` +
		"Search only for what the task needs, and don't search again for a tool whose parameters you already have.\n\nAvailable tools:")
	t.m.mu.RLock()
	for _, s := range t.m.servers {
		if s.direct {
			continue
		}
		fmt.Fprintf(&b, "\n\n## %s", s.name)
		if s.instructions != "" {
			b.WriteString("\n")
			b.WriteString(textfmt.CutRunes(strings.TrimSpace(s.instructions), maxIndexInstructions))
		}
		for _, x := range s.tools {
			line, _, _ := strings.Cut(strings.TrimSpace(x.tool.Load().Description), "\n")
			fmt.Fprintf(&b, "\n- %s: %s", x.name, textfmt.CutRunes(line, maxIndexDescription))
		}
	}
	t.m.mu.RUnlock()
	return model.ToolSchema{
		Name:        SearchToolName,
		Description: b.String(),
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"query":{"type":"string","description":"\"select:name1,name2\" for exact names, or keywords"},` +
			`"max_results":{"type":"integer","description":"keyword queries only; default 5"}},` +
			`"required":["query"]}`),
	}
}

// Risky is false: searching reads the index only.
func (t *SearchTool) Risky() bool { return false }

// FoundPrefix starts each tool's section in tool_search's result; traces
// parse it.
const FoundPrefix = "### "

// Run returns the definitions of the tools a query names ("select:a,b")
// or, failing an exact name, the best keyword matches.
func (t *SearchTool) Run(_ context.Context, args json.RawMessage) (tool.Result, error) {
	var in struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Result{}, err
	}
	all := t.m.Tools()
	var found []*Tool
	var missing []string
	if names, ok := strings.CutPrefix(strings.TrimSpace(in.Query), "select:"); ok {
		for name := range strings.SplitSeq(names, ",") {
			name = strings.TrimSpace(name)
			if x := lookup(all, name); x != nil {
				found = append(found, x)
			} else if name != "" {
				missing = append(missing, name)
			}
		}
	} else {
		found = exact(all, in.Query)
		if len(found) == 0 {
			// A model can send a non-positive max_results: use the default.
			found = match(all, in.Query, cmp.Or(max(in.MaxResults, 0), defaultSearchResults))
		}
	}

	var b strings.Builder
	if len(missing) > 0 {
		fmt.Fprintf(&b, "not found: %s\n", strings.Join(missing, ", "))
	}
	if len(found) == 0 {
		return tool.Result{Content: b.String() + "No tools matched. Check the names in the " + SearchToolName + " description.", IsError: true}, nil
	}
	fmt.Fprintf(&b, "Call these through %s, e.g. {\"name\": %q, \"arguments\": {...}}.\n", CallToolName, found[0].name)
	for _, x := range found {
		st := x.tool.Load()
		params, _ := json.Marshal(st.InputSchema)
		fmt.Fprintf(&b, "\n%s%s\n%s\nParameters: %s\n", FoundPrefix, x.name, strings.TrimSpace(st.Description), params)
	}
	return tool.Result{Content: b.String()}, nil
}

// lookup finds a tool by its full name, else by its name without the
// mcp__<server>__ prefix when only one server has it. Full names first: a
// server could otherwise name a tool mcp__other__x to catch calls meant for
// the other server's x.
func lookup(tools []*Tool, name string) *Tool {
	for _, x := range tools {
		if x.name == name {
			return x
		}
	}
	var found *Tool
	for _, x := range tools {
		if bareName(x.name) == name {
			if found != nil {
				return nil // ambiguous: the full name is needed
			}
			found = x
		}
	}
	return found
}

// bareName is a tool's name without its mcp__<server>__ prefix.
func bareName(name string) string {
	_, bare, _ := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__")
	return bare
}

// exact returns the tools a query word names exactly, with or without the
// mcp__<server>__ prefix: models often search for a name they already know.
func exact(tools []*Tool, query string) []*Tool {
	words := strings.Fields(query)
	var out []*Tool
	for _, x := range tools {
		if slices.Contains(words, x.name) || slices.Contains(words, bareName(x.name)) {
			out = append(out, x)
		}
	}
	return out
}

// match ranks tools by the query words found in their own name (weighted
// higher) or description, keeping index order among equals. Words in the
// server's name are ignored: they match every tool of that server.
func match(tools []*Tool, query string, limit int) []*Tool {
	words := strings.Fields(strings.ToLower(query))
	type hit struct {
		tool  *Tool
		score int
	}
	var hits []hit
	for _, x := range tools {
		server, name, _ := strings.Cut(strings.TrimPrefix(strings.ToLower(x.name), "mcp__"), "__")
		desc := strings.ToLower(x.tool.Load().Description)
		score := 0
		for _, w := range words {
			switch {
			case strings.Contains(server, w):
			case strings.Contains(name, w):
				score += 3
			case strings.Contains(desc, w):
				score++
			}
		}
		if score > 0 {
			hits = append(hits, hit{x, score})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return b.score - a.score })
	var out []*Tool
	for _, h := range hits[:min(len(hits), limit)] {
		out = append(out, h.tool)
	}
	return out
}
