package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/tool"
)

// TestMain doubles as a stdio MCP server, so Connect is tested against a
// real child process.
func TestMain(m *testing.M) {
	if os.Getenv("WISP_TEST_MCP_SERVER") == "1" {
		serve()
		return
	}
	os.Exit(m.Run())
}

func serve() {
	s := sdk.NewServer(&sdk.Implementation{Name: "fake"}, &sdk.ServerOptions{Instructions: "Amounts are in cents."})
	type echoIn struct {
		Text string `json:"text"`
	}
	sdk.AddTool(s, &sdk.Tool{Name: "echo", Description: "Echo text back.\nSecond line.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "echo: " + in.Text}}}, nil, nil
		})
	sdk.AddTool(s, &sdk.Tool{Name: "delete_account", Description: "Delete a budget account."},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "no such account"}}, IsError: true}, nil, nil
		})
	sdk.AddTool(s, &sdk.Tool{Name: "rename_account", Description: "Rename an account.", Annotations: &sdk.ToolAnnotations{DestructiveHint: new(false)}},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "renamed"}}}, nil, nil
		})
	sdk.AddTool(s, &sdk.Tool{Name: "balance", Description: "Current balance of an account."},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{}, nil, nil
		})
	sdk.AddTool(s, &sdk.Tool{Name: "add_tool", Description: "Add a tool named late."},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			sdk.AddTool(s, &sdk.Tool{Name: "late", Description: "Added at runtime."},
				func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
					return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "late"}}}, nil, nil
				})
			return &sdk.CallToolResult{}, nil, nil
		})
	_ = s.Run(context.Background(), &sdk.StdioTransport{})
}

func connectFake(t *testing.T) *Manager {
	t.Helper()
	m, errs := Connect(context.Background(), map[string]ServerConfig{
		"budget": {Command: os.Args[0], Env: map[string]string{"WISP_TEST_MCP_SERVER": "1"}},
		"broken": {Command: filepath.Join(t.TempDir(), "missing")},
	}, 10*time.Second, nil)
	t.Cleanup(m.Close)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `"broken"`) {
		t.Fatalf("errs = %v, want only the broken server", errs)
	}
	return m
}

type coreTool struct{ name string }

func (c coreTool) Schema() model.ToolSchema { return model.ToolSchema{Name: c.name} }
func (coreTool) Risky() bool                { return false }
func (coreTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{}, nil
}

func names(schemas []model.ToolSchema) string {
	var out []string
	for _, s := range schemas {
		out = append(out, s.Name)
	}
	return strings.Join(out, " ")
}

// call runs one tool call through the dispatcher, as a loop would.
func call(t *testing.T, reg *tool.Registry, name, args string) tool.Result {
	t.Helper()
	r := tool.Dispatch(context.Background(), reg, []model.ToolCall{{ID: "1", Name: name, Args: json.RawMessage(args)}}, nil)[0]
	if r.Err != nil {
		t.Fatalf("%s: %v", name, r.Err)
	}
	return r.Result
}

func allow(t *Tool) tool.Tool { return permission.Gate{Tool: t, Prompter: permission.AllowAll{}} }

// mcpRegistry is a loop's tool list with m's two MCP tools.
func mcpRegistry(m *Manager, gate func(*Tool) tool.Tool) *tool.Registry {
	return tool.NewRegistry(coreTool{"read"}, NewSearchTool(m), NewCallTool(m, gate))
}

func TestSearchDescribesToolsWithoutChangingTheToolList(t *testing.T) {
	m := connectFake(t)
	reg := mcpRegistry(m, allow)
	before := names(reg.Schemas())

	search, _ := reg.Get(SearchToolName)
	desc := search.Schema().Description
	for _, want := range []string{"## budget", "Amounts are in cents.", "- mcp__budget__echo: Echo text back.", "- mcp__budget__delete_account: "} {
		if !strings.Contains(desc, want) {
			t.Errorf("index lacks %q:\n%s", want, desc)
		}
	}
	if strings.Contains(desc, "Second line") {
		t.Error("index should keep only the first description line")
	}

	res := call(t, reg, SearchToolName, `{"query":"select:mcp__budget__echo, nope"}`)
	for _, want := range []string{"not found: nope", "### mcp__budget__echo\nEcho text back.\nSecond line.\nParameters: {", `"text"`} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("select result lacks %q:\n%s", want, res.Content)
		}
	}
	if got := names(reg.Schemas()); got != before || got != "mcp_call read tool_search" {
		t.Errorf("tool list = %q, want it unchanged (%q)", got, before)
	}
	if !strings.Contains(Prompt(m), "Connected: budget.") {
		t.Errorf("prompt = %q", Prompt(m))
	}
}

func TestCallProxiesAndValidates(t *testing.T) {
	m := connectFake(t)
	reg := mcpRegistry(m, allow)
	for _, name := range []string{"mcp__budget__echo", "echo"} {
		if res := call(t, reg, CallToolName, `{"name":"`+name+`","arguments":{"text":"hi"}}`); res.Content != "echo: hi" {
			t.Errorf("%s = %+v", name, res)
		}
	}
	res := call(t, reg, CallToolName, `{"name":"echo","arguments":{"text":5}}`)
	if !res.IsError || !strings.Contains(res.Content, "invalid arguments for mcp__budget__echo") || !strings.Contains(res.Content, "Parameters: {") {
		t.Errorf("invalid args = %+v, want an error with the schema", res)
	}
	if res := call(t, reg, CallToolName, `{"name":"nope"}`); !res.IsError || !strings.Contains(res.Content, "unknown MCP tool") {
		t.Errorf("unknown tool = %+v", res)
	}
	if res := call(t, reg, CallToolName, `{"name":"delete_account"}`); !res.IsError || res.Content != "no such account" {
		t.Errorf("tool errors should reach the model as errors: %+v", res)
	}

	proxy := NewCallTool(m, allow)
	for args, want := range map[string]bool{
		`{"name":"echo"}`:           false, // read-only: runs alongside other calls
		`{"name":"delete_account"}`: true,
		`{"name":"nope"}`:           true,
		`not json`:                  true,
	} {
		if got := proxy.RiskyCall(json.RawMessage(args)); got != want {
			t.Errorf("RiskyCall(%s) = %v, want %v", args, got, want)
		}
	}
}

func TestToolListChangedRefreshesIndex(t *testing.T) {
	m := connectFake(t)
	reg := mcpRegistry(m, allow)
	call(t, reg, CallToolName, `{"name":"add_tool"}`)
	search, _ := reg.Get(SearchToolName)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(search.Schema().Description, "mcp__budget__late: Added at runtime.") {
		if time.Now().After(deadline) {
			t.Fatalf("index never picked up the new tool:\n%s", search.Schema().Description)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if res := call(t, reg, CallToolName, `{"name":"late"}`); res.Content != "late" {
		t.Errorf("late = %+v", res)
	}
	// A tool that stayed is still callable.
	if res := call(t, reg, CallToolName, `{"name":"add_tool"}`); res.IsError {
		t.Errorf("add_tool after refresh = %+v", res)
	}
}

func TestSearchRanking(t *testing.T) {
	m := connectFake(t)
	reg := mcpRegistry(m, allow)
	found := func(query string) string {
		res := call(t, reg, SearchToolName, query)
		var out []string
		for line := range strings.Lines(res.Content) {
			if name, ok := strings.CutPrefix(strings.TrimSpace(line), FoundPrefix); ok {
				out = append(out, name)
			}
		}
		return strings.Join(out, " ")
	}
	// "balance" comes first in the index, but only delete_account has "account" in its name.
	if got := found(`{"query":"account","max_results":1}`); got != "mcp__budget__delete_account" {
		t.Errorf("a name match should outrank a description match: %q", got)
	}
	// An exact name, bare or prefixed, returns just that tool, though
	// "balance" and "rename_account" mention accounts too.
	for _, q := range []string{"delete_account", "mcp__budget__delete_account"} {
		if got := found(`{"query":"` + q + `"}`); got != "mcp__budget__delete_account" {
			t.Errorf("query %q found %q", q, got)
		}
	}
	// The server's name matches every tool, so it adds nothing.
	if res := call(t, reg, SearchToolName, `{"query":"budget zebra"}`); !res.IsError {
		t.Errorf("server-name words alone should not match: %+v", res)
	}
}

type askSpy struct{ asked []string }

func (a *askSpy) Prompt(name string, _ json.RawMessage) permission.Decision {
	a.asked = append(a.asked, name)
	return permission.Deny
}

func TestSkippingPermissionsStillAsksForDestructiveTools(t *testing.T) {
	m := connectFake(t)
	ask := &askSpy{}
	reg := mcpRegistry(m, Gate(permission.AllowAll{}, ask))
	if res := call(t, reg, CallToolName, `{"name":"rename_account"}`); res.Content != "renamed" {
		t.Errorf("destructiveHint false should run unasked: %+v", res)
	}
	if res := call(t, reg, CallToolName, `{"name":"delete_account"}`); res.Content != permission.DeniedContent {
		t.Errorf("a tool without annotations should ask: %+v", res)
	}
	if strings.Join(ask.asked, " ") != "mcp__budget__delete_account" {
		t.Errorf("asked = %v, want only delete_account", ask.asked)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	global, project := filepath.Join(dir, "global.json"), filepath.Join(dir, "project.json")
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(global, `{"mcpServers": {
		"a": {"command": "a-server"},
		"b": {"url": "https://b.example/mcp", "headers": {"Authorization": "Bearer ${WISP_TEST_TOKEN}"}},
		"c": {"command": "c-server"}}}`)
	write(project, `{"mcpServers": {"a": {"command": "a-local", "args": ["--root", "$WISP_TEST_TOKEN"]}, "c": {"command": "c-server", "disabled": true}}}`)
	t.Setenv("WISP_TEST_TOKEN", "secret")

	got, err := LoadConfig(nil, global, project, filepath.Join(dir, "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("servers = %v, want a and b", got)
	}
	if got["a"].Command != "a-local" || got["a"].Args[1] != "secret" {
		t.Errorf("a = %+v, want the project entry, expanded", got["a"])
	}
	if got["b"].Headers["Authorization"] != "Bearer secret" {
		t.Errorf("b headers = %v", got["b"].Headers)
	}

	write(project, `{"mcpServers": {"d": {"command": "x", "url": "https://x"}}}`)
	if _, err := LoadConfig(nil, project); err == nil {
		t.Error("a server with both command and url should be rejected")
	}
}

func TestToolNameIsProviderSafe(t *testing.T) {
	if got := toolName("my.server", "get/thing"); got != "mcp__my_server__get_thing" {
		t.Errorf("got %q", got)
	}
	if got := toolName("s", strings.Repeat("x", 100)); len(got) != 64 {
		t.Errorf("len = %d, want 64", len(got))
	}
}

func TestLookupPrefersFullNames(t *testing.T) {
	spoof, real, other := &Tool{name: "mcp__a__mcp__b__delete"}, &Tool{name: "mcp__b__delete"}, &Tool{name: "mcp__c__delete"}
	tools := []*Tool{spoof, real, other}
	if got := lookup(tools, "mcp__b__delete"); got != real {
		t.Errorf("full name found %v", got)
	}
	if got := lookup(tools, "delete"); got != nil {
		t.Errorf("an ambiguous bare name found %s", got.name)
	}
	if got := lookup(tools[:2], "delete"); got != real {
		t.Errorf("a unique bare name found %v", got)
	}
}
