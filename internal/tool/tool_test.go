package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

// fakeTool runs fn and reports risky via a fixed flag, for exercising the
// registry/dispatcher without a real built-in tool.
type fakeTool struct {
	name  string
	risky bool
	fn    func(ctx context.Context, args json.RawMessage) (Result, error)
}

func (f *fakeTool) Schema() model.ToolSchema { return model.ToolSchema{Name: f.name} }
func (f *fakeTool) Risky() bool              { return f.risky }
func (f *fakeTool) Run(ctx context.Context, args json.RawMessage) (Result, error) {
	return f.fn(ctx, args)
}

// callRiskTool is risky only for calls whose args are "true".
type callRiskTool struct{ *fakeTool }

func (callRiskTool) RiskyCall(args json.RawMessage) bool { return string(args) == "true" }

func noop(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }

func TestRegistry(t *testing.T) {
	reg := NewRegistry(&fakeTool{name: "write", risky: true, fn: noop}, &fakeTool{name: "echo", fn: noop})

	if _, ok := reg.Get("missing"); ok {
		t.Error("Get(missing) = ok, want not found")
	}
	if got, ok := reg.Get("write"); !ok || !got.Risky() {
		t.Errorf("Get(write) = %+v, ok=%v, want the risky write tool", got, ok)
	}
	var names []string
	for _, s := range reg.Schemas() {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, ","); got != "echo,write" {
		t.Errorf("Schemas() = %s, want sorted by name", got)
	}
	if _, ok := (*Registry)(nil).Get("echo"); ok {
		t.Error("a nil Registry found a tool")
	}
}

func TestIsRisky(t *testing.T) {
	plain := &fakeTool{name: "w", risky: true, fn: noop}
	perCall := callRiskTool{&fakeTool{name: "r", risky: true, fn: noop}}
	if !IsRisky(plain, nil) {
		t.Error("a Risky tool was not risky")
	}
	if IsRisky(perCall, json.RawMessage("false")) || !IsRisky(perCall, json.RawMessage("true")) {
		t.Error("RiskyCall did not decide over Risky")
	}
}

func TestChildEnvDropsAPIKey(t *testing.T) {
	t.Setenv("WISP_API_KEY", "secret")
	t.Setenv("WISP_OTHER", "kept")
	env := strings.Join(ChildEnv(), "\n")
	if strings.Contains(env, "WISP_API_KEY") || !strings.Contains(env, "WISP_OTHER=kept") {
		t.Errorf("ChildEnv kept the key or dropped other variables")
	}
}
