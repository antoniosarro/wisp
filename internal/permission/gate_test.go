package permission

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

func TestGateSkipsPromptForNonRiskyTool(t *testing.T) {
	underlying, spy := &fakeTool{}, &spyPrompter{}
	res, err := Gate{Tool: underlying, Prompter: spy}.Run(context.Background(), nil)
	if err != nil || spy.called || !underlying.ran || res.Content != "ran" {
		t.Errorf("Run = %+v, %v; asked %v, ran %v; want it run without asking", res, err, spy.called, underlying.ran)
	}
}

func TestGateAllowsRiskyCall(t *testing.T) {
	underlying, spy := &fakeTool{risky: true}, &spyPrompter{allow: true}
	res, err := Gate{Tool: underlying, Prompter: spy}.Run(context.Background(), nil)
	if err != nil || !spy.called || !underlying.ran || res.Content != "ran" {
		t.Errorf("Run = %+v, %v; asked %v, ran %v; want it asked, then run", res, err, spy.called, underlying.ran)
	}
}

func TestGateDeniesRiskyCall(t *testing.T) {
	underlying := &fakeTool{risky: true}
	res, err := Gate{Tool: underlying, Prompter: &spyPrompter{}}.Run(context.Background(), nil)
	if err != nil || underlying.ran || !res.IsError || res.Content != DeniedContent {
		t.Errorf("Run = %+v, %v; ran %v; want a denied error result", res, err, underlying.ran)
	}
}

func TestGateDenialCarriesNote(t *testing.T) {
	res, err := Gate{Tool: bashLike{}, Prompter: notePrompter{}}.Run(context.Background(), commandArgs("npm i"))
	if err != nil || !res.IsError || res.Content != DeniedContent+". The user says: use pnpm" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
}

func TestGateAllowAllRunsWithoutAsking(t *testing.T) {
	underlying := &fakeTool{risky: true}
	if _, err := (Gate{Tool: underlying, Prompter: AllowAll{}}).Run(context.Background(), nil); err != nil || !underlying.ran {
		t.Errorf("Run = %v, ran %v; want it run", err, underlying.ran)
	}
}

// A turn cancelled while the user was being asked is an error, not a
// denial the model would read and react to.
func TestGateCancelledWhileAskingIsNotADenial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	underlying := &fakeTool{risky: true}
	p := promptFunc(func(string, json.RawMessage) Decision { cancel(); return Deny })
	if _, err := (Gate{Tool: underlying, Prompter: p}).Run(ctx, nil); !errors.Is(err, context.Canceled) || underlying.ran {
		t.Errorf("Run = %v, ran %v; want context.Canceled", err, underlying.ran)
	}
}

func TestGatePreservesSchemaAndRisk(t *testing.T) {
	g := Gate{Tool: &fakeTool{risky: true}}
	if g.Schema().Name != "fake" || !g.Risky() {
		t.Errorf("Schema().Name = %q, Risky() = %v; want the wrapped tool's", g.Schema().Name, g.Risky())
	}
	r := Gate{Tool: riskyReadTool{}}
	if r.RiskyCall(json.RawMessage(`{"path":"main.go"}`)) || !r.RiskyCall(json.RawMessage(`{"path":".env"}`)) {
		t.Error("RiskyCall did not pass the wrapped tool's per-call risk on")
	}
}

func TestGateAllSharesOneRuleSet(t *testing.T) {
	spy := &spyPrompter{always: true}
	gated := GateAll(spy, bashLike{}, bashLike{})
	for _, g := range gated {
		if _, ok := g.(Gate); !ok {
			t.Fatalf("tool %+v is not wrapped in a Gate", g)
		}
	}
	_, _ = gated[0].Run(context.Background(), commandArgs("ls"))
	spy.called = false
	_, _ = gated[1].Run(context.Background(), commandArgs("ls -la"))
	if spy.called {
		t.Error("an always-allow through one gated tool didn't cover the other")
	}
}

// Reading credentials asks, through the gate, and "always" covers that
// file only.
func TestGateAsksForCredentialReads(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	asked := 0
	g := Gate{Tool: riskyReadTool{}, Prompter: promptFunc(func(string, json.RawMessage) Decision { asked++; return AllowAlways }), Rules: &Rules{}}
	for _, path := range []string{"main.go", "~/x", ".env", ".env", "b/.env"} {
		if _, err := g.Run(context.Background(), json.RawMessage(`{"path":"`+path+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if asked != 2 {
		t.Errorf("asked %d times, want 2 (.env once, b/.env once)", asked)
	}
}

// requiredTool requires a path.
type requiredTool struct{ fakeTool }

func (*requiredTool) Schema() model.ToolSchema {
	return model.ToolSchema{Name: "req", Parameters: json.RawMessage(`{"type":"object","required":["path"]}`)}
}

func TestGateRejectsMissingArgumentsBeforeAsking(t *testing.T) {
	for _, args := range []string{`{}`, `{"path":null}`, ``, `[]`} {
		spy := &spyPrompter{allow: true}
		underlying := &requiredTool{fakeTool{risky: true}}
		if _, err := (Gate{Tool: underlying, Prompter: spy}).Run(context.Background(), json.RawMessage(args)); err == nil || spy.called || underlying.ran {
			t.Errorf("args %q: err %v, asked %v, ran %v; want an error before asking", args, err, spy.called, underlying.ran)
		}
	}
	if _, err := (Gate{Tool: &requiredTool{}, Prompter: AllowAll{}}).Run(context.Background(), json.RawMessage(`{"path":""}`)); err != nil {
		t.Errorf("an empty string is a value: %v", err)
	}
}

var _ tool.Tool = Gate{}
