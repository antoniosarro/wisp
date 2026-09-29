package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

const explorerFile = `---
name: explorer
description: Read-only code search.
model: small
tools: [echo]
max_iterations: 5
---
You search code and report file:line references.
`

func TestParse(t *testing.T) {
	spec, err := parse([]byte(explorerFile))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "explorer" || spec.Model != "small" || len(spec.Tools) != 1 || spec.MaxIterations != 5 ||
		spec.Instructions != "You search code and report file:line references." {
		t.Fatalf("spec = %+v", spec)
	}
	for name, bad := range map[string]string{
		"no front matter": "just text",
		"unclosed":        "---\nname: x\n",
		"unknown field":   "---\nname: x\ndescription: d\nmodle: typo\n---\nbody",
		"bad name":        "---\nname: Bad Name\ndescription: d\n---\nbody",
		"no description":  "---\nname: x\n---\nbody",
		"no instructions": "---\nname: x\ndescription: d\n---\n",
	} {
		if _, err := parse([]byte(bad)); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func TestLoadProjectOverridesGlobal(t *testing.T) {
	global, project := t.TempDir(), t.TempDir()
	write := func(dir, desc string) {
		body := strings.Replace(explorerFile, "Read-only code search.", desc, 1)
		if err := os.WriteFile(filepath.Join(dir, "explorer.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(global, "global")
	write(project, "project")
	specs, err := Load(global, project, filepath.Join(t.TempDir(), "missing"))
	if err != nil || len(specs) != 1 || specs[0].Description != "project" {
		t.Fatalf("Load() = %+v, %v", specs, err)
	}
}

// originTool records which sub-agent its calls came from.
type originTool struct{ origin *string }

func (originTool) Schema() model.ToolSchema { return model.ToolSchema{Name: "echo"} }
func (originTool) Risky() bool              { return false }
func (o originTool) Run(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	*o.origin = permission.Origin(ctx)
	return tool.Result{Content: "found it"}, nil
}

func newTestTool(t *testing.T, p model.Provider, observer func(Event), tools ...tool.Tool) *Tool {
	t.Helper()
	spec, err := parse([]byte(explorerFile))
	if err != nil {
		t.Fatal(err)
	}
	at, err := NewTool(Options{
		Specs:       []Spec{spec},
		Tools:       tool.NewRegistry(tools...),
		NewProvider: func(Spec) model.Provider { return p },
		System:      func(s Spec) string { return s.Instructions },
		MaxParallel: 1,
		Observer:    observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestRunReturnsReportAndReportsProgress(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventReasoningDelta, Reasoning: "hm"}, {Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "s1", Name: "echo", Args: json.RawMessage(`{}`)}}, {Kind: model.EventDone}},
		{{Kind: model.EventTextDelta, Text: "Loop is in "}, {Kind: model.EventTextDelta, Text: "core/loop.go:29"}, {Kind: model.EventDone}},
	}}
	var events []Event
	var origin string
	at := newTestTool(t, p, func(e Event) { events = append(events, e) }, originTool{&origin})

	res, err := at.Run(context.Background(), json.RawMessage(`{"agent":"explorer","task":"find Loop"}`))
	if err != nil || res.IsError || res.Content != "Loop is in core/loop.go:29" {
		t.Fatalf("Run() = %+v, %v", res, err)
	}
	if origin != "explorer" {
		t.Errorf("sub-agent tool calls carried origin %q, want explorer", origin)
	}
	if p.LastReq.Messages[0].Content != "You search code and report file:line references." {
		t.Errorf("sub-agent system prompt = %q", p.LastReq.Messages[0].Content)
	}

	last := events[len(events)-1]
	if last.Steps[0].Output != "found it" || last.Text != last.Report {
		t.Errorf("step output %q, text %q; want the tool result and the report", last.Steps[0].Output, last.Text)
	}
	if events[0].Status != Waiting || last.Status != Done || last.Report != res.Content || len(last.Steps) != 1 || last.Steps[0].Status != "ok" {
		t.Fatalf("first event %+v, last event %+v", events[0], last)
	}
	// Streaming tokens must not flood the observer: two text deltas are one change.
	writing := 0
	for i, e := range events {
		if e.Activity == "writing report" && events[i-1].Activity != "writing report" {
			writing++
		}
	}
	if writing != 1 || len(events) > 10 {
		t.Errorf("got %d switches to writing and %d events in total; want 1 and few", writing, len(events))
	}
}

func TestRunFailureIsAToolError(t *testing.T) {
	at := newTestTool(t, &testutil.ScriptedProvider{}, nil, testutil.EchoTool{})
	res, err := at.Run(context.Background(), json.RawMessage(`{"agent":"explorer","task":"x"}`))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "sub-agent explorer failed") {
		t.Fatalf("Run() = %+v, %v", res, err)
	}
	if _, err := at.Run(context.Background(), json.RawMessage(`{"agent":"nobody","task":"x"}`)); err == nil {
		t.Fatal("unknown agent accepted")
	}
}

func TestNewToolRejectsUnknownTools(t *testing.T) {
	spec, _ := parse([]byte(explorerFile))
	_, err := NewTool(Options{Specs: []Spec{spec}, Tools: tool.NewRegistry(), NewProvider: func(Spec) model.Provider { return nil }})
	if err == nil || !strings.Contains(err.Error(), `unknown tool "echo"`) {
		t.Fatalf("err = %v", err)
	}
}

// blockingProvider holds each request until released.
type blockingProvider struct{ release chan struct{} }

func (b blockingProvider) Stream(ctx context.Context, _ model.Request) (<-chan model.Event, error) {
	ch := make(chan model.Event, 2)
	go func() {
		defer close(ch)
		select {
		case <-b.release:
			ch <- model.Event{Kind: model.EventTextDelta, Text: "ok"}
			ch <- model.Event{Kind: model.EventDone}
		case <-ctx.Done():
			ch <- model.Event{Kind: model.EventError, Err: ctx.Err()}
		}
	}()
	return ch, nil
}

func TestParallelLimitQueuesRuns(t *testing.T) {
	p := blockingProvider{release: make(chan struct{})}
	var mu sync.Mutex
	latest := map[int64]Status{}
	at := newTestTool(t, p, func(e Event) { mu.Lock(); latest[e.RunID] = e.Status; mu.Unlock() }, testutil.EchoTool{})

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = at.Run(context.Background(), json.RawMessage(`{"agent":"explorer","task":"x"}`))
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		running, waiting := 0, 0
		for _, s := range latest {
			switch s {
			case Running:
				running++
			case Waiting:
				waiting++
			}
		}
		mu.Unlock()
		if running == 1 && waiting == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("statuses = %v, want one running and one waiting", latest)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(p.release)
	wg.Wait()
}

func TestCancelWhileWaiting(t *testing.T) {
	p := blockingProvider{release: make(chan struct{})}
	at := newTestTool(t, p, nil, testutil.EchoTool{})
	go func() { _, _ = at.Run(context.Background(), json.RawMessage(`{"agent":"explorer","task":"hold"}`)) }()
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := at.Run(ctx, json.RawMessage(`{"agent":"explorer","task":"x"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	close(p.release)
}

func TestTraceForwardsTheWholeConversation(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "s1", Name: "echo", Args: json.RawMessage(`{}`)}}, {Kind: model.EventDone}},
		{{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventDone}},
	}}
	var events, results int
	spec, _ := parse([]byte(explorerFile))
	at, err := NewTool(Options{
		Specs:       []Spec{spec},
		Tools:       tool.NewRegistry(testutil.EchoTool{}),
		NewProvider: func(Spec) model.Provider { return p },
		System:      func(Spec) string { return "" },
		Trace: func(tr Trace) {
			if tr.Event != nil {
				events++
			}
			if tr.Result != nil && tr.Result.ToolCall.ID == "s1" {
				results++
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := at.Run(context.Background(), json.RawMessage(`{"agent":"explorer","task":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if events != 4 || results != 1 {
		t.Fatalf("traced %d events and %d results, want 4 and 1", events, results)
	}
}

func TestRunCostAddsBilledAndListPriced(t *testing.T) {
	var last Event
	r := &run{observer: func(e Event) { last = e }}
	r.onStats(core.StepStats{UsageAvailable: true, SessionPromptTokens: 10, Cost: 0.02, CostReported: true, CostEstimate: 0.03})
	if last.Cost != 0.02 || last.CostEstimated {
		t.Fatalf("after a billed request: %+v", last)
	}
	r.onStats(core.StepStats{UsageAvailable: true, SessionPromptTokens: 30, CostEstimate: 0.01})
	if last.Cost < 0.0299 || last.Cost > 0.0301 || !last.CostEstimated || last.PromptTokens != 30 {
		t.Fatalf("after a list-priced request: %+v", last)
	}
	r.onStats(core.StepStats{UsageAvailable: true, SessionPromptTokens: 40}) // no price: adds nothing
	if last.Cost < 0.0299 || last.Cost > 0.0301 {
		t.Fatalf("after an unpriced request: %+v", last)
	}
}

// bigTool returns more output than a small window can hold. Named echo so
// the explorer spec, which lists echo, may use it.
type bigTool struct{}

func (bigTool) Schema() model.ToolSchema { return model.ToolSchema{Name: "echo"} }
func (bigTool) Risky() bool              { return false }
func (bigTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: strings.Repeat("line of output\n", 15000)}, nil
}

// A sub-agent's loop knows its model's window, so its results are clipped
// to fit like the main agent's.
func TestRunKnowsItsWindow(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "s1", Name: "echo", Args: json.RawMessage(`{}`)}}, {Kind: model.EventDone}},
		{{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventDone}},
	}}
	spec, _ := parse([]byte(explorerFile))
	at, err := NewTool(Options{
		Specs:       []Spec{spec},
		Tools:       tool.NewRegistry(bigTool{}),
		NewProvider: func(Spec) model.Provider { return p },
		System:      func(Spec) string { return "" },
		Info:        func(Spec) model.Info { return model.Info{ContextWindow: 16384} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := at.Run(context.Background(), json.RawMessage(`{"agent":"explorer","task":"x"}`)); err != nil {
		t.Fatal(err)
	}
	msgs := p.LastReq.Messages
	if got := len(msgs[len(msgs)-1].Content); got > 50_000 {
		t.Errorf("the sub-agent sent a %d-byte result into a 16K window", got)
	}
}

// A sub-agent whose model can't call tools runs without them, as the main
// agent does.
func TestRunWithoutToolsWhenTheModelHasNone(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventDone}}}}
	spec, _ := parse([]byte(explorerFile))
	at, err := NewTool(Options{
		Specs:       []Spec{spec},
		Tools:       tool.NewRegistry(testutil.EchoTool{}),
		NewProvider: func(Spec) model.Provider { return p },
		System:      func(Spec) string { return "" },
		Info:        func(Spec) model.Info { return model.Info{Tools: model.Unsupported} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := at.Run(context.Background(), json.RawMessage(`{"agent":"explorer","task":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if p.LastReq.Tools != nil {
		t.Errorf("sent %d tools to a model that can't call them", len(p.LastReq.Tools))
	}
}
