package core

import (
	"time"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tokencount"
)

// StepStats snapshots one provider round-trip plus session totals. Token
// counts are provider-reported; *Est fields are local-tokenizer estimates,
// for the parts of a response the provider doesn't break down.
type StepStats struct {
	UsageAvailable bool // the provider reported usage for this step
	// This step; zero if the provider reported no usage.
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int     // part of PromptTokens served from the provider's cache
	Cost             float64 // US dollars billed, when CostReported
	CostReported     bool
	CostEstimate     float64 // at the loop's list Price, cached tokens at the cached rate; 0 without a price
	Provider         string  // upstream provider that served it, when reported

	// Session totals, including this step.
	SessionPromptTokens     int
	SessionCompletionTokens int
	SessionCachedTokens     int
	SessionCost             float64 // billed costs reported so far

	// Request breakdown.
	SystemTokensEst  int
	HistoryTokensEst int
	ToolDefTokensEst int

	// Response breakdown.
	ReasoningTokensEst int
	AnswerTokensEst    int
	ToolCallTokensEst  int // tool names and arguments

	Duration     time.Duration // from sending the request to the end of the stream
	TokensPerSec float64       // completion tokens over Duration; 0 without usage
	RequestCount int           // requests this session, including this one

	Context ContextStats // the budget after this step
}

// ContextStats is the context budget as the loop sees it, in estimated
// server tokens (local estimates times the calibration ratio). A zero
// Window means the window is unknown.
type ContextStats struct {
	Window  int     `json:"window"`
	Fixed   int     `json:"fixed"`   // system prompt and tool definitions
	Budget  int     `json:"budget"`  // what history may use
	History int     `json:"history"` // what history uses
	Ratio   float64 `json:"ratio"`   // server tokens per local estimate token

	MaskedResults int    `json:"masked_results"` // tool results sent as stand-ins
	MaskedCalls   int    `json:"masked_calls"`   // write and edit calls sent without their bodies
	MaskedSaved   int    `json:"masked_saved"`   // tokens masking saves
	Compactions   int    `json:"compactions"`
	Presummary    string `json:"presummary,omitempty"` // "", "generating", or "ready"

	// Where the tokens go. Fixed is Prompt, Project, MCPPrompt, and the
	// tools; History is Summary and Messages.
	Prompt    int            `json:"prompt"`     // the system prompt's own text
	Project   int            `json:"project"`    // project instructions (AGENTS.md)
	MCPPrompt int            `json:"mcp_prompt"` // the system prompt's MCP servers section
	Tools     map[string]int `json:"tools"`      // each tool definition, by name
	Summary   int            `json:"summary"`    // the compaction summary
	Messages  int            `json:"messages"`   // messages sent verbatim or masked
	Reserve   int            `json:"reserve"`    // room kept for the reply; 0 when the window is unknown
}

// MaskTrigger and SummarizeTrigger are the history sizes, in tokens, past
// which old tool output is masked, and history summarized, before the next
// request.
func (c ContextStats) MaskTrigger() int      { return c.Budget * maskTriggerPct / 100 }
func (c ContextStats) SummarizeTrigger() int { return c.Budget * summarizePct / 100 }

// CacheHitRate is the share of prompt tokens served from cache, as a
// percentage; ok is false when nothing was reported.
func CacheHitRate(cached, prompt int) (pct float64, ok bool) {
	if prompt <= 0 {
		return 0, false
	}
	return float64(cached) / float64(prompt) * 100, true
}

// response is what one step streamed, for its stats.
type response struct {
	reasoning, answer, toolCalls string
	usage                        *model.Usage // nil when the provider reported none
	duration                     time.Duration
}

// addUsage counts a request's usage in the session's totals.
func (l *Loop) addUsage(u model.Usage) {
	l.stats.SessionPromptTokens += u.PromptTokens
	l.stats.SessionCompletionTokens += u.CompletionTokens
	l.stats.SessionCachedTokens += u.CachedTokens
	l.stats.SessionCost += u.Cost
}

// recordStats folds r's usage into the session totals and reports a
// snapshot with local-tokenizer estimates of the breakdown. Without
// OnStats, nothing is counted: estimating costs a tokenizer pass.
func (l *Loop) recordStats(r response) {
	if l.OnStats == nil {
		return
	}

	l.stats.RequestCount++
	if r.usage != nil {
		l.addUsage(*r.usage)
	}

	l.fixedTokens() // fills the system and tool-definition estimates below
	snapshot := StepStats{
		Context:                 l.contextStats(),
		SystemTokensEst:         l.tokens.systemTokens,
		HistoryTokensEst:        l.historyTokens(),
		ToolDefTokensEst:        l.tokens.toolDefs,
		SessionPromptTokens:     l.stats.SessionPromptTokens,
		SessionCompletionTokens: l.stats.SessionCompletionTokens,
		SessionCachedTokens:     l.stats.SessionCachedTokens,
		SessionCost:             l.stats.SessionCost,
		ReasoningTokensEst:      tokencount.Count(r.reasoning),
		AnswerTokensEst:         tokencount.Count(r.answer),
		ToolCallTokensEst:       tokencount.Count(r.toolCalls),
		Duration:                r.duration,
		RequestCount:            l.stats.RequestCount,
	}
	if u := r.usage; u != nil {
		snapshot.UsageAvailable = true
		snapshot.PromptTokens = u.PromptTokens
		snapshot.CompletionTokens = u.CompletionTokens
		snapshot.CachedTokens = u.CachedTokens
		snapshot.Cost, snapshot.CostReported, snapshot.Provider = u.Cost, u.CostReported, u.Provider
		if l.Price.Known {
			snapshot.CostEstimate = l.Price.Cost(u.PromptTokens, u.CachedTokens, u.CompletionTokens)
		}
		if r.duration > 0 {
			snapshot.TokensPerSec = float64(u.CompletionTokens) / r.duration.Seconds()
		}
	}

	l.OnStats(snapshot)
}
