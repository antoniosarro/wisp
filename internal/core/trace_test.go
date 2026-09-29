package core

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/span"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

// spanSink keeps the latest write of each span.
type spanSink struct {
	mu    sync.Mutex
	spans map[string]span.Record
}

func (s *spanSink) WriteSpan(r span.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans[r.ID] = r
	return nil
}

// childSpanTool opens a span of its own, as the permission gate, MCP, and
// sub-agents do.
type childSpanTool struct{}

func (childSpanTool) Schema() model.ToolSchema { return model.ToolSchema{Name: "child"} }
func (childSpanTool) Risky() bool              { return false }
func (childSpanTool) Run(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	_, sp := span.Start(ctx, span.KindMCP, "inner")
	sp.End(span.StatusOK)
	return tool.Result{Content: "ok"}, nil
}

func TestLoopRecordsSpans(t *testing.T) {
	calls := []model.Event{
		{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "1", Name: "child", Args: json.RawMessage(`{}`)}},
		{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "2", Name: "child", Args: json.RawMessage(`{}`)}},
		{Kind: model.EventDone, Usage: &model.Usage{PromptTokens: 100, CachedTokens: 80, CompletionTokens: 7}},
	}
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{calls, {{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventDone}}}}
	sink := &spanSink{spans: map[string]span.Record{}}
	rec := span.NewRecorder(sink, "s1")
	l := &Loop{Provider: p, Tools: tool.NewRegistry(childSpanTool{}), Spans: rec}
	if _, err := l.Run(context.Background(), "do it\nplease"); err != nil {
		t.Fatal(err)
	}
	rec.Close()

	byKind := map[string][]span.Record{}
	for _, r := range sink.spans {
		byKind[r.Kind] = append(byKind[r.Kind], r)
	}
	turn := byKind[span.KindTurn]
	if len(turn) != 1 || turn[0].Name != "do it" || turn[0].Status != span.StatusOK || turn[0].Attrs["wisp.input"] != "do it\nplease" {
		t.Fatalf("turn = %+v", turn)
	}
	reqs := byKind[span.KindRequest]
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	for _, r := range reqs {
		if r.Parent != turn[0].ID {
			t.Errorf("request parent %q, want the turn", r.Parent)
		}
		if r.Attrs["gen_ai.usage.input_tokens"] == 100 && r.Attrs["wisp.usage.cached_tokens"] != 80 {
			t.Errorf("request usage = %+v", r.Attrs)
		}
		var c struct {
			Fixed    int  `json:"fixed"`
			Messages int  `json:"messages"`
			MaskAt   *int `json:"mask_at"`
		}
		if b, _ := json.Marshal(r.Attrs["wisp.context"]); json.Unmarshal(b, &c) != nil || c.Fixed == 0 || c.Messages == 0 || c.MaskAt == nil {
			t.Errorf("request context = %s", b)
		}
	}
	tools := byKind[span.KindTool]
	if len(tools) != 2 {
		t.Fatalf("tools = %+v", tools)
	}
	statuses := map[string]string{}
	for _, tl := range tools {
		statuses[tl.Attrs["gen_ai.tool.call.id"].(string)] = tl.Status
		if tl.Parent != turn[0].ID {
			t.Errorf("tool parent %q, want the turn", tl.Parent)
		}
	}
	if statuses["1"] != span.StatusOK || statuses["2"] != span.StatusSkipped {
		t.Errorf("tool statuses = %v, want the repeat skipped", statuses)
	}
	inner := byKind[span.KindMCP]
	ranID := ""
	for _, tl := range tools {
		if tl.Status == span.StatusOK {
			ranID = tl.ID
		}
	}
	if len(inner) != 1 || inner[0].Parent != ranID {
		t.Errorf("inner span = %+v, want it under the tool that ran", inner)
	}
}

func TestRequestCostOnSpans(t *testing.T) {
	answer := func(u *model.Usage) []model.Event {
		return []model.Event{{Kind: model.EventTextDelta, Text: "ok"}, {Kind: model.EventDone, Usage: u}}
	}
	usage := model.Usage{PromptTokens: 1_000_000, CachedTokens: 400_000, CompletionTokens: 100_000}
	billed := usage
	billed.Cost, billed.CostReported = 0.5, true
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{answer(&usage), answer(&billed), answer(&usage)}}
	sink := &spanSink{spans: map[string]span.Record{}}
	rec := span.NewRecorder(sink, "s")
	var steps []StepStats
	l := &Loop{Provider: p, Tools: tool.NewRegistry(), Spans: rec,
		Price:   model.Pricing{Known: true, Input: 1, Output: 4, CachedInput: 0.1},
		OnStats: func(s StepStats) { steps = append(steps, s) }}
	for _, in := range []string{"list", "billed"} {
		if _, err := l.Run(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	l.Price = model.Pricing{}
	if _, err := l.Run(context.Background(), "unpriced"); err != nil {
		t.Fatal(err)
	}
	rec.Close()

	// 600k uncached at $1 + 400k cached at $0.10 + 100k out at $4 = 0.6 + 0.04 + 0.4.
	const list = 1.04
	byTurn := map[string]span.Record{}
	for _, r := range sink.spans {
		if r.Kind == span.KindRequest {
			byTurn[sink.spans[r.Parent].Name] = r
		}
	}
	near := func(v any, want float64) bool { f, ok := v.(float64); return ok && f > want-1e-9 && f < want+1e-9 }
	a := byTurn["list"].Attrs
	if a["wisp.cost.source"] != "list price" || !near(a["wisp.cost.usd"], list) || !near(a["wisp.cost.input_usd"], 0.6) ||
		!near(a["wisp.cost.cached_input_usd"], 0.04) || !near(a["wisp.cost.output_usd"], 0.4) || a["wisp.usage.cost_usd"] != nil {
		t.Errorf("list-priced request = %v", a)
	}
	a = byTurn["billed"].Attrs
	if a["wisp.cost.source"] != "billed" || !near(a["wisp.cost.usd"], 0.5) || !near(a["wisp.usage.cost_usd"], 0.5) || !near(a["wisp.cost.list_usd"], list) {
		t.Errorf("billed request = %v", a)
	}
	if a := byTurn["unpriced"].Attrs; a["wisp.cost.source"] != "unknown" || a["wisp.cost.usd"] != nil {
		t.Errorf("unpriced request = %v", a)
	}
	if len(steps) != 3 || steps[0].CostEstimate < list-1e-9 || steps[0].CostEstimate > list+1e-9 || steps[2].CostEstimate != 0 {
		t.Errorf("step estimates = %+v", steps)
	}
}

// A summary request is traced as a request span of its own, marked as a
// compaction, with its usage.
func TestSummaryRequestIsTraced(t *testing.T) {
	sink := &spanSink{spans: map[string]span.Record{}}
	rec := span.NewRecorder(sink, "s")
	p := &billedProvider{replyProvider{replies: []string{"## Goal\n- x"}}}
	l := &Loop{Provider: p, ContextWindow: 16384, History: turns(12), Spans: rec}
	if err := l.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	rec.Close()
	var found bool
	for _, r := range sink.spans {
		if r.Kind == span.KindRequest && r.Attrs["wisp.compaction"] == true {
			found = r.Name == "compact" && r.Attrs["gen_ai.usage.input_tokens"] == 9000 && strings.Contains(r.Attrs["wisp.answer"].(string), "- x")
		}
	}
	if !found {
		t.Errorf("no compaction request span with its usage and summary: %+v", sink.spans)
	}
}
