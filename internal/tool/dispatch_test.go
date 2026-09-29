package tool

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

func TestDispatchPreservesOrder(t *testing.T) {
	// slow finishes after fast despite starting first, to prove Dispatch
	// returns results in call order, not completion order.
	slow := &fakeTool{name: "slow", fn: func(context.Context, json.RawMessage) (Result, error) {
		time.Sleep(30 * time.Millisecond)
		return Result{Content: "slow"}, nil
	}}
	fast := &fakeTool{name: "fast", fn: func(context.Context, json.RawMessage) (Result, error) {
		return Result{Content: "fast"}, nil
	}}
	results := Dispatch(context.Background(), NewRegistry(slow, fast), []model.ToolCall{{Name: "slow"}, {Name: "fast"}}, nil)
	if len(results) != 2 || results[0].Result.Content != "slow" || results[1].Result.Content != "fast" {
		t.Errorf("results = %+v, want order [slow, fast] matching the call order", results)
	}
}

func TestDispatchReportsCompletionBeforeBatchEnds(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	completed := make(chan string, 2)
	slow := &fakeTool{name: "slow", fn: func(context.Context, json.RawMessage) (Result, error) { <-release; return Result{}, nil }}
	fast := &fakeTool{name: "fast", fn: noop}
	go Dispatch(context.Background(), NewRegistry(slow, fast), []model.ToolCall{{Name: "slow"}, {Name: "fast"}}, func(r CallResult) { completed <- r.ToolCall.Name })
	select {
	case name := <-completed:
		if name != "fast" {
			t.Fatal("wrong completion", name)
		}
	case <-time.After(time.Second):
		t.Fatal("completion blocked behind slow tool")
	}
}

// A risky call waits for the reads before it and holds back those after:
// "read, write, read" must observe the write only in the second read.
func TestDispatchRiskyCallsAreBarriers(t *testing.T) {
	var mu sync.Mutex
	var log []string
	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		log = append(log, s)
	}
	read := &fakeTool{name: "read", fn: func(_ context.Context, a json.RawMessage) (Result, error) {
		time.Sleep(20 * time.Millisecond) // would finish after the write if not waited for
		record("read" + string(a))
		return Result{}, nil
	}}
	write := &fakeTool{name: "write", risky: true, fn: func(_ context.Context, a json.RawMessage) (Result, error) {
		record("write" + string(a))
		return Result{}, nil
	}}
	Dispatch(context.Background(), NewRegistry(read, write), []model.ToolCall{
		{Name: "read", Args: json.RawMessage(`1`)},
		{Name: "write", Args: json.RawMessage(`2`)},
		{Name: "write", Args: json.RawMessage(`3`)},
		{Name: "read", Args: json.RawMessage(`4`)},
	}, nil)
	if len(log) != 4 || log[0] != "read1" || log[1] != "write2" || log[2] != "write3" || log[3] != "read4" {
		t.Errorf("order = %v, want [read1 write2 write3 read4]", log)
	}
}

func TestDispatchErrors(t *testing.T) {
	panics := &fakeTool{name: "panics", fn: func(context.Context, json.RawMessage) (Result, error) { panic("boom") }}
	results := Dispatch(context.Background(), NewRegistry(panics), []model.ToolCall{{Name: "nope"}, {Name: "panics"}}, nil)
	if results[0].Err == nil {
		t.Error("unknown tool: no error")
	}
	if results[1].Err == nil {
		t.Error("a panicking tool: no error, want the panic turned into one")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	never := &fakeTool{name: "never", fn: func(context.Context, json.RawMessage) (Result, error) { ran = true; return Result{}, nil }}
	if results := Dispatch(ctx, NewRegistry(never), []model.ToolCall{{Name: "never"}}, nil); results[0].Err == nil || ran {
		t.Errorf("cancelled turn: ran %v, err %v; want the call skipped with an error", ran, results[0].Err)
	}
}

func TestCallID(t *testing.T) {
	var got string
	id := &fakeTool{name: "id", fn: func(ctx context.Context, _ json.RawMessage) (Result, error) {
		got = CallID(ctx)
		return Result{}, nil
	}}
	Dispatch(context.Background(), NewRegistry(id), []model.ToolCall{{ID: "call_7", Name: "id"}}, nil)
	if got != "call_7" {
		t.Errorf("CallID = %q, want the call's ID", got)
	}
}
