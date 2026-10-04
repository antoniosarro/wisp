package mcp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/tool"
)

func TestBuiltinStartsGoplsInAGoModule(t *testing.T) {
	bin := t.TempDir()
	gopls := filepath.Join(bin, "gopls")
	if err := os.WriteFile(gopls, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(module, "internal", "x")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}

	g, ok := Builtin(pkg)["gopls"]
	if !ok || g.Command != gopls || !slices.Equal(g.Args, []string{"mcp"}) || !g.Direct || !g.ReadOnly || g.Instructions == nil {
		t.Errorf("in a module's package: gopls = %+v, %v", g, ok)
	}
	if got := Builtin(t.TempDir()); got != nil {
		t.Errorf("outside a module: %v", got)
	}
	t.Setenv("PATH", t.TempDir())
	if got := Builtin(pkg); got != nil {
		t.Errorf("without gopls on PATH: %v", got)
	}
}

func TestConfigReplacesOrDisablesBuiltin(t *testing.T) {
	project := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(project, []byte(`{"mcpServers": {"gopls": {"command": "gopls", "disabled": true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	builtin := map[string]ServerConfig{"gopls": {Command: "gopls", Args: []string{"mcp"}}, "other": {Command: "other"}}
	got, err := LoadConfig(builtin, project)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["gopls"]; ok || got["other"].Command != "other" {
		t.Errorf("servers = %v, want gopls disabled and other kept", got)
	}
	if _, ok := builtin["gopls"]; !ok {
		t.Error("LoadConfig changed the builtin map")
	}
}

// A direct server's tools are in the tool list, not the index; its
// instructions replace the server's own and go to the system prompt; and
// read-only, its unannotated tools run without asking.
func TestDirectReadOnlyServer(t *testing.T) {
	instructions := "Use echo to repeat text."
	fake := func(cfg ServerConfig) ServerConfig {
		cfg.Command, cfg.Env = os.Args[0], map[string]string{"WISP_TEST_MCP_SERVER": "1"}
		return cfg
	}
	m, errs := Connect(context.Background(), map[string]ServerConfig{
		"budget": fake(ServerConfig{}),
		"code":   fake(ServerConfig{Direct: true, ReadOnly: true, Instructions: &instructions}),
	}, 10*time.Second, nil)
	t.Cleanup(m.Close)
	if len(errs) > 0 {
		t.Fatal(errs)
	}

	direct := m.DirectTools()
	if len(direct) == 0 || slices.ContainsFunc(direct, func(x *Tool) bool { return !strings.HasPrefix(x.name, "mcp__code__") }) {
		t.Errorf("direct tools = %v, want code's only", direct)
	}
	if !m.Deferred() {
		t.Error("budget is deferred")
	}
	if index := NewSearchTool(m).Schema().Description; strings.Contains(index, "mcp__code__") || !strings.Contains(index, "mcp__budget__echo") {
		t.Errorf("index should list budget's tools only:\n%s", index)
	}
	prompt := Prompt(m)
	if !strings.Contains(prompt, "Connected: budget.") || !strings.Contains(prompt, "## code\n") || !strings.Contains(prompt, instructions) || strings.Contains(prompt, "Amounts are in cents.") {
		t.Errorf("prompt:\n%s", prompt)
	}

	// An interactive user who allowed nothing: every prompt is denied.
	prompted, ask := &askSpy{}, &askSpy{}
	gate := Gate(prompted, ask)
	var tools []tool.Tool
	for _, x := range direct {
		tools = append(tools, gate(x))
	}
	reg := tool.NewRegistry(tools...)
	if res := call(t, reg, "mcp__code__delete_account", `{}`); res.Content == permission.DeniedContent || len(prompted.asked)+len(ask.asked) > 0 {
		t.Errorf("a read-only server's tool asked (%v %v): %+v", prompted.asked, ask.asked, res)
	}
}
