package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

func TestRendererStreamsTheTurn(t *testing.T) {
	var out strings.Builder
	onEvent, onToolResult := newRenderer(&out)
	for _, e := range []model.Event{
		{Kind: model.EventReasoningDelta, Reasoning: "need a listing"},
		{Kind: model.EventTextDelta, Text: "Let me look."},
		{Kind: model.EventToolCall, ToolCall: &model.ToolCall{Name: "ls", Args: json.RawMessage(`{}`)}},
		{Kind: model.EventToolCall}, // no call: nothing to print
		{Kind: model.EventDone},
	} {
		onEvent(e)
	}
	onToolResult(model.ToolCall{Name: "ls"}, tool.Result{Content: "a.go"}, nil)
	onToolResult(model.ToolCall{Name: "bash"}, tool.Result{Content: "exit 1", IsError: true}, nil)
	onToolResult(model.ToolCall{Name: "nope"}, tool.Result{}, errors.New("unknown tool"))

	want := dim + "need a listing" + reset + "Let me look.\n[tool call] ls({})\n" +
		"[tool result] ls (ok): a.go\n[tool result] bash (error): exit 1\n[tool result] nope: error: unknown tool\n"
	if out.String() != want {
		t.Errorf("rendered\n%q\nwant\n%q", out.String(), want)
	}
}

// Text from the model or a tool must not drive the terminal.
func TestRendererStripsEscapes(t *testing.T) {
	var out strings.Builder
	onEvent, onToolResult := newRenderer(&out)
	onEvent(model.Event{Kind: model.EventTextDelta, Text: "hi\x1b]52;c;cGF5bG9hZA==\x07"})
	onToolResult(model.ToolCall{Name: "bash"}, tool.Result{Content: "\x1b[2Jcleared"}, nil)
	if strings.Contains(out.String(), "\x1b]") || strings.Contains(out.String(), "\x1b[2J") {
		t.Errorf("rendered %q with escape sequences", out.String())
	}
}

func TestUsageSummary(t *testing.T) {
	price := model.Pricing{Known: true, Input: 3, Output: 15}
	for _, c := range []struct {
		name  string
		stats core.StepStats
		info  model.Info
		want  string
	}{
		{"no requests", core.StepStats{}, model.Info{}, ""},
		{"billed", core.StepStats{RequestCount: 2, SessionPromptTokens: 1000, SessionCachedTokens: 800, SessionCompletionTokens: 50, SessionCost: 0.0123}, model.Info{},
			"wisp: 2 requests, prompt 1000 (cached 800, 80.0%), completion 50, billed $0.0123\n"},
		{"estimated", core.StepStats{RequestCount: 1, SessionPromptTokens: 1_000_000, SessionCompletionTokens: 100_000}, model.Info{Price: price},
			"wisp: 1 requests, prompt 1000000 (cache unreported), completion 100000, est. cost $4.5000\n"},
	} {
		var out strings.Builder
		printUsageSummary(&out, c.stats, c.info)
		if out.String() != c.want {
			t.Errorf("%s: %q, want %q", c.name, out.String(), c.want)
		}
	}
}
