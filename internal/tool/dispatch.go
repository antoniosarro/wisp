package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/span"
)

// callIDKey is the context key the running call's ID is stored under.
type callIDKey struct{}

// CallID returns the ID of the tool call ctx belongs to, if any.
func CallID(ctx context.Context) string {
	id, _ := ctx.Value(callIDKey{}).(string)
	return id
}

// CallResult pairs a requested ToolCall with its outcome.
type CallResult struct {
	ToolCall model.ToolCall
	Result   Result
	Err      error
}

// Dispatch runs a turn's calls and returns their results in request order,
// whatever order they finish in, so the conversation stays deterministic.
// Read-only calls run concurrently; a risky call is a barrier, waiting for
// the calls before it and holding back those after, so writes and
// commands apply in the order the model asked for them. onComplete
// (optional) runs serially, in completion order, as each call finishes.
// Tools bound their own time: bash and fetch have timeouts, MCP calls a
// limit, and a sub-agent may rightly run long.
func Dispatch(ctx context.Context, reg *Registry, calls []model.ToolCall, onComplete func(CallResult)) []CallResult {
	results := make([]CallResult, len(calls))
	var wg sync.WaitGroup
	var callbackMu sync.Mutex
	run := func(i int, call model.ToolCall) {
		results[i] = runCall(ctx, reg, call)
		if onComplete != nil {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			onComplete(results[i])
		}
	}

	for i, call := range calls {
		if t, ok := reg.Get(call.Name); ok && IsRisky(t, call.Args) {
			wg.Wait()
			run(i, call)
			continue
		}
		wg.Go(func() { run(i, call) })
	}
	wg.Wait()
	return results
}

// runCall runs one call in its own span, recording its arguments, result,
// and status the way OpenTelemetry's GenAI conventions name them.
func runCall(ctx context.Context, reg *Registry, call model.ToolCall) CallResult {
	r := CallResult{ToolCall: call}
	callCtx := context.WithValue(ctx, callIDKey{}, call.ID)
	callCtx, sp := span.Start(callCtx, span.KindTool, call.Name)
	sp.Set("gen_ai.operation.name", "execute_tool")
	sp.Set("gen_ai.tool.name", call.Name)
	sp.Set("gen_ai.tool.call.id", call.ID)
	sp.Set("gen_ai.tool.call.arguments", string(call.Args))
	if err := callCtx.Err(); err != nil {
		r.Err = err // the turn was cancelled before this call started
	} else if t, ok := reg.Get(call.Name); !ok {
		r.Err = fmt.Errorf("unknown tool %q", call.Name)
	} else {
		sp.Set("wisp.risky", IsRisky(t, call.Args))
		r.Result, r.Err = runSafely(callCtx, t, call.Args)
	}
	sp.Set("gen_ai.tool.call.result", r.Result.Content)
	if len(r.Result.Images) > 0 {
		sp.Set("wisp.images", len(r.Result.Images))
	}
	if r.Err == nil && r.Result.IsError {
		sp.End(span.StatusError)
	} else {
		sp.EndErr(callCtx, r.Err)
	}
	return r
}

// runSafely runs t, turning a panic into the call's error: one broken tool
// must not take down the process, and with it the terminal's state.
func runSafely(ctx context.Context, t Tool, args json.RawMessage) (res Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			res, err = Result{}, fmt.Errorf("tool panicked: %v", p)
		}
	}()
	return t.Run(ctx, args)
}
