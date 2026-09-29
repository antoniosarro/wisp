package core

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

func TestLoopOnStatsReportsUsageAndAccumulates(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{
			{Kind: model.EventTextDelta, Text: "hi"},
			{Kind: model.EventDone, Usage: &model.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}},
		},
		{
			{Kind: model.EventTextDelta, Text: "again"},
			{Kind: model.EventDone, Usage: &model.Usage{PromptTokens: 20, CompletionTokens: 4, TotalTokens: 24}},
		},
	}}

	var got []StepStats
	l := &Loop{Provider: p, Tools: tool.NewRegistry(), OnStats: func(s StepStats) {
		got = append(got, s)
	}}

	if _, err := l.Run(context.Background(), "one"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := l.Run(context.Background(), "two"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("OnStats called %d times, want 2", len(got))
	}

	first, second := got[0], got[1]
	if !first.UsageAvailable || first.PromptTokens != 10 || first.CompletionTokens != 2 {
		t.Errorf("first step real tokens = %+v, want prompt=10 completion=2", first)
	}
	if first.SessionPromptTokens != 10 || first.SessionCompletionTokens != 2 {
		t.Errorf("first step session totals = %+v, want prompt=10 completion=2", first)
	}
	if first.RequestCount != 1 {
		t.Errorf("first step RequestCount = %d, want 1", first.RequestCount)
	}
	if first.AnswerTokensEst == 0 {
		t.Error("first step AnswerTokensEst = 0, want a nonzero estimate for \"hi\"")
	}

	if second.SessionPromptTokens != 30 || second.SessionCompletionTokens != 6 {
		t.Errorf("second step session totals = %+v, want prompt=30 completion=6 (accumulated)", second)
	}
	if second.RequestCount != 2 {
		t.Errorf("second step RequestCount = %d, want 2", second.RequestCount)
	}
}

func TestLoopAccumulatesCachedTokens(t *testing.T) {
	turn := func(prompt, cached int) []model.Event {
		return []model.Event{{Kind: model.EventDone, Usage: &model.Usage{PromptTokens: prompt, CachedTokens: cached}}}
	}
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{turn(100, 0), turn(200, 180)}}
	l := &Loop{Provider: p}
	var last StepStats
	l.OnStats = func(s StepStats) { last = s }

	for range 2 {
		if _, err := l.Run(context.Background(), "hi"); err != nil {
			t.Fatal(err)
		}
	}
	if last.CachedTokens != 180 || last.SessionCachedTokens != 180 || last.SessionPromptTokens != 300 {
		t.Errorf("stats = %+v, want 180 cached this step, 180 of 300 for the session", last)
	}
	if pct, ok := CacheHitRate(last.SessionCachedTokens, last.SessionPromptTokens); !ok || pct != 60 {
		t.Errorf("CacheHitRate = %v, %v; want 60, true", pct, ok)
	}
	if _, ok := CacheHitRate(0, 0); ok {
		t.Error("CacheHitRate(0, 0) reported a rate with no prompt tokens")
	}
}

// The endpoint's billed cost is reported as is; the list price gives an
// estimate next to it, and billed costs add up over the session.
func TestStatsCost(t *testing.T) {
	usage := &model.Usage{PromptTokens: 1_000_000, CachedTokens: 400_000, CompletionTokens: 100_000, Cost: 2.5, CostReported: true, Provider: "DeepInfra"}
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventDone, Usage: usage}},
		{{Kind: model.EventDone, Usage: usage}},
	}}
	var last StepStats
	l := &Loop{Provider: p, Price: model.Pricing{Known: true, Input: 3, Output: 15, CachedInput: 0.3}, OnStats: func(s StepStats) { last = s }}
	for range 2 {
		if _, err := l.Run(context.Background(), "q"); err != nil {
			t.Fatal(err)
		}
	}
	if !last.CostReported || last.Cost != 2.5 || last.SessionCost != 5 || last.Provider != "DeepInfra" {
		t.Errorf("billed = %+v, want 2.5 this step, 5 for the session, from DeepInfra", last)
	}
	if math.Abs(last.CostEstimate-3.42) > 1e-9 { // 1.8 uncached + 0.12 cached + 1.5 output
		t.Errorf("CostEstimate = %v, want 3.42 at the list price", last.CostEstimate)
	}
}

func TestStatsBreakDownTheResponse(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{
			{Kind: model.EventReasoningDelta, Reasoning: "the user wants a file listing"},
			{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c", Name: "echo", Args: json.RawMessage(`{"path":"."}`)}},
			{Kind: model.EventDone},
		},
		{
			{Kind: model.EventTextDelta, Text: "draft"},
			{Kind: model.EventReclassify}, // "draft" was reasoning
			{Kind: model.EventTextDelta, Text: "done"},
			{Kind: model.EventDone, Usage: &model.Usage{CompletionTokens: 10}},
		},
	}}
	var got []StepStats
	l := &Loop{Provider: p, Tools: tool.NewRegistry(testutil.EchoTool{}), OnStats: func(s StepStats) { got = append(got, s) }}
	if _, err := l.Run(context.Background(), "ls"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("OnStats called %d times, want 2", len(got))
	}
	first, second := got[0], got[1]
	if first.UsageAvailable || first.ReasoningTokensEst == 0 || first.ToolCallTokensEst == 0 || first.AnswerTokensEst != 0 {
		t.Errorf("first step = %+v, want reasoning and a tool call, no answer, no usage", first)
	}
	if second.ReasoningTokensEst == 0 || second.AnswerTokensEst == 0 || second.Duration <= 0 || second.Duration > time.Second {
		t.Errorf("second step = %+v, want the reclassified draft counted as reasoning", second)
	}
	if second.TokensPerSec <= 0 {
		t.Errorf("TokensPerSec = %v, want completion tokens over the step's duration", second.TokensPerSec)
	}
}
