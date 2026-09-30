package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/model/openaicompat"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/termsafe"
	"github.com/antoniosarro/wisp/internal/tool"
)

// Terminal styles for what isn't the answer: reasoning and tool activity.
const (
	dim   = "\x1b[2m"
	reset = "\x1b[0m"
)

// runSingleShot runs one turn, streaming to stdout and prompting on stdin
// for risky tool calls, then reports the session's usage on stderr.
func runSingleShot(ctx context.Context, cfg Config, provider *openaicompat.Client, info model.Info, prompt string) error {
	loop, cleanup, err := newLoop(cfg, provider, info, new(atomic.Bool), permission.TerminalPrompter{}, printAgentEvent, nil)
	if err != nil {
		return err
	}
	defer cleanup()

	loop.Presummarize = false // the process exits after one turn: nothing would use it
	loop.OnEvent, loop.OnToolResult = newRenderer(os.Stdout)
	var last core.StepStats
	loop.OnStats = func(s core.StepStats) { last = s }
	_, err = loop.Run(ctx, prompt)
	fmt.Println()
	printUsageSummary(os.Stderr, last, info)
	return err
}

// printUsageSummary writes session token totals to w, so logs of
// non-interactive runs (benchmarks) record them. Cost is what the endpoint
// billed when it said, else an estimate at the model's list price.
func printUsageSummary(w io.Writer, s core.StepStats, info model.Info) {
	if s.RequestCount == 0 {
		return
	}
	cache := "cache unreported"
	if pct, ok := core.CacheHitRate(s.SessionCachedTokens, s.SessionPromptTokens); ok && s.SessionCachedTokens > 0 {
		cache = fmt.Sprintf("cached %d, %.1f%%", s.SessionCachedTokens, pct)
	}
	cost := ""
	switch {
	case s.SessionCost > 0:
		cost = fmt.Sprintf(", billed $%.4f", s.SessionCost)
	case info.Price.Known:
		cost = fmt.Sprintf(", est. cost $%.4f", info.Price.Cost(s.SessionPromptTokens, s.SessionCachedTokens, s.SessionCompletionTokens))
	}
	_, _ = fmt.Fprintf(w, "wisp: %d requests, prompt %d (%s), completion %d%s\n",
		s.RequestCount, s.SessionPromptTokens, cache, s.SessionCompletionTokens, cost)
}

// printAgentEvent reports when sub-agents start and finish; their own
// steps aren't printed, only the report the main agent gets.
func printAgentEvent(e agent.Event) {
	switch {
	case e.Status == agent.Running && e.Activity == "starting":
		fmt.Printf("\n%s[agent %s] %s%s\n", dim, e.Agent, termsafe.Strip(e.Task), reset)
	case e.Status == agent.Done:
		fmt.Printf("%s[agent %s] done in %s after %d tool call(s)%s\n", dim, e.Agent, e.Took.Round(time.Second), len(e.Steps), reset)
	case e.Status == agent.Failed:
		fmt.Printf("%s[agent %s] failed: %s%s\n", dim, e.Agent, termsafe.Strip(fmt.Sprint(e.Err)), reset)
	}
}

// newRenderer returns loop callbacks writing streamed text, dimmed
// reasoning, and tool activity to w. Everything from the model or a tool
// goes through termsafe.Strip: it must not drive the terminal.
func newRenderer(w io.Writer) (onEvent func(model.Event), onToolResult func(model.ToolCall, tool.Result, error)) {
	inReasoning := false
	onEvent = func(e model.Event) {
		if e.Kind == model.EventReasoningDelta {
			if !inReasoning {
				_, _ = fmt.Fprint(w, dim)
				inReasoning = true
			}
			_, _ = fmt.Fprint(w, termsafe.Strip(e.Reasoning))
			return
		}
		if inReasoning {
			_, _ = fmt.Fprint(w, reset)
			inReasoning = false
		}
		switch e.Kind {
		case model.EventTextDelta:
			_, _ = fmt.Fprint(w, termsafe.Strip(e.Text))
		case model.EventToolCall:
			if e.ToolCall != nil {
				_, _ = fmt.Fprintf(w, "\n[tool call] %s(%s)\n", termsafe.Strip(e.ToolCall.Name), termsafe.Strip(string(e.ToolCall.Args)))
			}
		}
	}

	onToolResult = func(call model.ToolCall, res tool.Result, err error) {
		name := termsafe.Strip(call.Name)
		switch {
		case err != nil:
			_, _ = fmt.Fprintf(w, "[tool result] %s: error: %s\n", name, termsafe.Strip(err.Error()))
		case res.IsError:
			_, _ = fmt.Fprintf(w, "[tool result] %s (error): %s\n", name, termsafe.Strip(res.Content))
		default:
			_, _ = fmt.Fprintf(w, "[tool result] %s (ok): %s\n", name, termsafe.Strip(res.Content))
		}
	}
	return onEvent, onToolResult
}
