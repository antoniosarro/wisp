package core

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tokencount"
	"github.com/antoniosarro/wisp/internal/tool"
)

// The context budget is what history may use of the window once the
// reply's reserve and the fixed cost of the system prompt and tool
// definitions are set aside. Limits are shares of it, so they scale from
// 16K to 1M windows.
const (
	resultPct = 25 // most of the budget one tool result may take

	messageOverhead = 4    // chat-template tokens around each message
	imageTokens     = 1024 // rough cost of one attached image

	ratioAlpha = 0.3 // weight of the newest sample in the calibration ratio
	ratioMin   = 0.7 // the ratio stays within these bounds, so one odd
	ratioMax   = 1.5 // report can't throw the estimates far off
)

// tokenCache keeps local token estimates between requests. History only
// grows, so each request counts just the messages added since the last.
// base is the History array counted: assigning History a new slice, as
// frontends and tests do, or append moving it, starts the count over.
type tokenCache struct {
	counted, history int            // messages counted, and their tokens
	base             *model.Message // &History[0] when counted
	system           string         // the system prompt counted
	systemTokens     int
	systemParts      [3]int         // the system prompt's own text, project instructions, MCP servers
	toolDefs         int            // all tool definitions
	toolTokens       map[string]int // each tool definition
	toolDefsCounted  int            // tools counted into toolDefs
	masked           int            // masked results and calls maskedSaved counts
	maskedSaved      int            // tokens masking saved, estimated
	ratio            float64        // backend tokens per estimated token; 0 until a request reports usage
}

// messageTokens estimates msg as a request carries it: content or its
// stand-in, tool calls with their arguments, and images.
func messageTokens(msg model.Message) int {
	msg = sent(msg)
	n := messageOverhead + tokencount.Count(msg.Content) + len(msg.Images)*imageTokens
	for _, call := range msg.ToolCalls {
		n += tokencount.Count(call.Name) + tokencount.Count(string(call.Args))
	}
	return n
}

// historyTokens estimates what requests send of History, the summary
// included, counting only messages added since the last call.
func (l *Loop) historyTokens() int {
	if len(l.History) == 0 {
		return l.summaryTokens()
	}
	if l.tokens.counted > len(l.History) || l.tokens.counted < l.keptFrom() || &l.History[0] != l.tokens.base {
		l.tokens.counted, l.tokens.base = 0, &l.History[0]
	}
	if l.tokens.counted == 0 {
		l.tokens.counted, l.tokens.history = l.keptFrom(), l.summaryTokens()
	}
	for _, msg := range l.History[l.tokens.counted:] {
		l.tokens.history += messageTokens(msg)
	}
	l.tokens.counted = len(l.History)
	return l.tokens.history
}

// fixedTokens estimates the system prompt and tool definitions, which
// every request carries and compaction can't shrink.
func (l *Loop) fixedTokens() int {
	if l.tokens.system != l.System {
		l.tokens.system = l.System
		prompt, project, mcp := systemSections(l.System)
		l.tokens.systemParts = [3]int{tokencount.Count(prompt), tokencount.Count(project), tokencount.Count(mcp)}
		if l.System != "" {
			l.tokens.systemParts[0] += messageOverhead // it is a message like the others
		}
		l.tokens.systemTokens = l.tokens.systemParts[0] + l.tokens.systemParts[1] + l.tokens.systemParts[2]
	}
	// The list changes only when MCP servers connect or the model does.
	if tools := l.requestTools(); len(tools) != l.tokens.toolDefsCounted || l.tokens.toolTokens == nil {
		l.tokens.toolTokens, l.tokens.toolDefs = make(map[string]int, len(tools)), 0
		for _, t := range tools {
			if b, err := json.Marshal(t); err == nil {
				n := tokencount.Count(string(b))
				l.tokens.toolTokens[t.Name] += n
				l.tokens.toolDefs += n
			}
		}
		l.tokens.toolDefsCounted = len(tools)
	}
	return l.tokens.systemTokens + l.tokens.toolDefs
}

// systemSections splits a system prompt into its own text, the project
// instructions prompt.Build adds, and the MCP section appended last.
func systemSections(s string) (prompt, project, mcp string) {
	if i := strings.LastIndex(s, "\n# MCP servers\n"); i >= 0 {
		s, mcp = s[:i], s[i:]
	}
	if i := strings.Index(s, "\n# Project instructions ("); i >= 0 {
		s, project = s[:i], s[i:]
	}
	return s, project, mcp
}

// requestTools is the tool list a request sends.
func (l *Loop) requestTools() []model.ToolSchema {
	if l.Tools == nil || l.NoTools {
		return nil
	}
	return l.Tools.Schemas()
}

// reserve is the room kept for the reply: an eighth of the window, within
// 4K to 16K tokens, and no more than the model can write.
func (l *Loop) reserve() int {
	r := min(max(l.ContextWindow/8, 4096), 16384)
	if l.MaxOutput > 0 {
		r = min(r, l.MaxOutput)
	}
	return r
}

// ratio is how many backend tokens one local estimate token is worth. The
// local tokenizer is cl100k, which differs from most local models' by
// 10–25%; reported prompt sizes correct it.
func (l *Loop) ratio() float64 {
	if l.tokens.ratio == 0 {
		return 1
	}
	return l.tokens.ratio
}

// calibrate folds a request's reported prompt size into the ratio;
// estimated is the local estimate of the same request. The ratio moves
// towards each sample, so it follows a model switch within a few requests.
func (l *Loop) calibrate(reported, estimated int) {
	if reported <= 0 || estimated <= 0 {
		return
	}
	sample := min(max(float64(reported)/float64(estimated), ratioMin), ratioMax)
	if l.tokens.ratio == 0 {
		l.tokens.ratio = sample
		return
	}
	l.tokens.ratio += ratioAlpha * (sample - l.tokens.ratio)
}

// historyBudget is how many backend tokens History may use: the window
// less room for the response and the fixed cost. It is 0 when the window
// is unknown.
func (l *Loop) historyBudget() int {
	if l.ContextWindow <= 0 {
		return 0
	}
	fixed := int(float64(l.fixedTokens()) * l.ratio())
	// When the fixed cost alone fills the window nothing fits, but a
	// budget of 0 would read as "unknown".
	return max(l.ContextWindow-l.reserve()-fixed, 1)
}

// projectedHistory is History's calibrated size as the next request would
// send it, counting what was appended since the last one.
func (l *Loop) projectedHistory() int {
	return int(float64(l.historyTokens()) * l.ratio())
}

// maskedSaved estimates the tokens masking saves, recounted only when
// what is masked changes.
func (l *Loop) maskedSaved() (masked, saved int) {
	for _, msg := range l.History {
		if msg.Elided != "" {
			masked++
		}
		for _, c := range msg.ToolCalls {
			if c.Elided != nil {
				masked++
			}
		}
	}
	if masked != l.tokens.masked {
		saved = 0
		for _, msg := range l.History {
			if msg.Elided != "" {
				saved += tokencount.Count(msg.Content) - tokencount.Count(msg.Elided)
			}
			for _, c := range msg.ToolCalls {
				if c.Elided != nil {
					saved += tokencount.Count(string(c.Args)) - tokencount.Count(string(c.Elided))
				}
			}
		}
		l.tokens.masked, l.tokens.maskedSaved = masked, saved
	}
	return masked, int(float64(l.tokens.maskedSaved) * l.ratio())
}

// clearAtLeast is the fewest tokens a proactive masking batch must free
// to be worth discarding the backend's prefix cache.
func (l *Loop) clearAtLeast() int {
	return max(2048, l.historyBudget()/10)
}

// Fit prepares History for the next request as a step does: past the mask
// trigger, it masks old tool output down to the target, in one batch worth
// the prefix cache it discards, and, with AutoCompact, summarizes past the
// summarize trigger. Evals use it to build the context a request would
// send without sending one. It fails only when ctx ends.
func (l *Loop) Fit(ctx context.Context) error {
	budget := l.historyBudget()
	if budget == 0 || l.projectedHistory() <= budget*maskTriggerPct/100 {
		return nil
	}
	l.mask(budget*maskTargetPct/100, true, l.clearAtLeast())
	if l.AutoCompact && l.projectedHistory() > budget*summarizePct/100 {
		// Compact falls back to the ledger when the summary fails, so
		// only ctx ending is an error here.
		_ = l.Compact(ctx, "")
	}
	return ctx.Err()
}

// ContextUsage snapshots the context budget and where its tokens go. It
// reads the loop's state, so frontends call it between turns and rely on
// the snapshot in StepStats during one.
func (l *Loop) ContextUsage() ContextStats { return l.contextStats() }

// contextStats snapshots the budget, in calibrated tokens.
func (l *Loop) contextStats() ContextStats {
	scale := func(n int) int { return int(float64(n) * l.ratio()) }
	c := ContextStats{
		Window:      l.ContextWindow,
		Fixed:       scale(l.fixedTokens()),
		Budget:      l.historyBudget(),
		History:     l.projectedHistory(),
		Ratio:       l.ratio(),
		Compactions: len(l.Compactions),
		Summary:     scale(l.summaryTokens()),
		Prompt:      scale(l.tokens.systemParts[0]),
		Project:     scale(l.tokens.systemParts[1]),
		MCPPrompt:   scale(l.tokens.systemParts[2]),
		Tools:       make(map[string]int, len(l.tokens.toolTokens)),
	}
	if c.Window > 0 {
		c.Reserve = l.reserve()
	}
	c.Messages = c.History - c.Summary
	for name, n := range l.tokens.toolTokens {
		c.Tools[name] = scale(n)
	}
	if pre := l.pre; pre != nil {
		c.Presummary = "generating"
		select {
		case <-pre.done:
			c.Presummary = "ready"
		default:
		}
	}
	_, c.MaskedSaved = l.maskedSaved()
	for _, msg := range l.History {
		if msg.Elided != "" {
			c.MaskedResults++
		}
		for _, call := range msg.ToolCalls {
			if call.Elided != nil {
				c.MaskedCalls++
			}
		}
	}
	return c
}

// minResultTokens is the least fitResult clips a result to.
const minResultTokens = 256

// fitResult clips a tool result to resultPct of the budget, keeping its
// head and tail and saving the whole to a file, so that one result can't
// fill a small window. Tools cap their output for large windows already.
func (l *Loop) fitResult(content string) string {
	if l.ContextWindow <= 0 {
		return content
	}
	// When the fixed cost nearly fills the window the budget rounds to
	// nothing, and that is when clipping matters most.
	limit := max(l.historyBudget()*resultPct/100, minResultTokens)
	// A token is at least a byte, so short output needs no counting.
	if float64(len(content))*l.ratio() <= float64(limit) {
		return content
	}
	n := float64(tokencount.Count(content)) * l.ratio()
	if n <= float64(limit) {
		return content
	}
	return tool.Clip(content, int(float64(len(content))*float64(limit)/n), 0)
}

// windowInError matches the window size servers state when rejecting an
// overlong request: vLLM and OpenAI ("maximum context length is 32768
// tokens") and llama.cpp ("exceeds the available context size (16384
// tokens)", "n_ctx":16384).
var windowInError = regexp.MustCompile(`(?i)(?:maximum context length is|available context size \(|"n_ctx"\s*:\s*)\s*(\d+)`)

// windowFromError returns the context window an overflow error states, or 0.
func windowFromError(err error) int {
	m := windowInError.FindStringSubmatch(err.Error())
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
