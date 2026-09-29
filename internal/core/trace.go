package core

import (
	"context"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/span"
)

// Tracing: each Run is a turn span, and each request a child span of it
// carrying what was sent, what came back, and what it cost. Attribute names
// follow OpenTelemetry's GenAI conventions where one exists (gen_ai.*).
// Tool calls are traced by tool.Dispatch, under the turn too.

// startRequestSpan opens the span of a request about to be sent.
func (l *Loop) startRequestSpan(ctx context.Context, req model.Request) (context.Context, *span.Span) {
	modelName := ""
	if m, ok := l.Provider.(interface{ Model() string }); ok {
		modelName = m.Model()
	}
	ctx, sp := span.Start(ctx, span.KindRequest, modelName)
	sp.Set("gen_ai.operation.name", "chat")
	sp.Set("gen_ai.request.model", modelName)
	sp.Set("gen_ai.request.max_tokens", req.MaxTokens)
	sp.Set("wisp.tool_choice", req.ToolChoice)
	sp.Set("wisp.messages", len(req.Messages))
	c := l.contextStats()
	sp.Set("wisp.context", struct {
		ContextStats
		MaskAt      int `json:"mask_at"`
		SummarizeAt int `json:"summarize_at"`
	}{c, c.MaskTrigger(), c.SummarizeTrigger()})
	toolNames := make([]string, len(req.Tools))
	for i, t := range req.Tools {
		toolNames[i] = t.Name
	}
	sp.Set("wisp.tools", toolNames)
	return ctx, sp
}

// endRequestSpan records what a request streamed back; the caller ends
// the span.
func (l *Loop) endRequestSpan(sp *span.Span, r response, firstToken time.Duration, toolCalls []model.ToolCall, truncated bool) {
	sp.Set("wisp.time_to_first_token_ms", firstToken.Milliseconds())
	sp.Set("wisp.reasoning", r.reasoning)
	sp.Set("wisp.answer", r.answer)
	sp.Set("wisp.truncated", truncated)
	callNames := make([]string, len(toolCalls))
	for i, c := range toolCalls {
		callNames[i] = c.Name
	}
	sp.Set("wisp.tool_calls", callNames)
	if u := r.usage; u != nil {
		recordUsage(sp, l.Price, u)
		if u.Provider != "" {
			sp.Set("wisp.provider", u.Provider)
		}
		if gen := r.duration - firstToken; gen > 0 {
			sp.Set("wisp.output_tokens_per_s", float64(u.CompletionTokens)/gen.Seconds())
		}
	}
}

// recordUsage puts a request's tokens and cost on its span. The cost is
// what the endpoint billed, when it says (exact), and what the list price
// comes to, split into uncached input, cached input, and output.
// wisp.cost.usd is the best of the two, wisp.cost.source says which.
func recordUsage(sp *span.Span, price model.Pricing, u *model.Usage) {
	sp.Set("gen_ai.usage.input_tokens", u.PromptTokens)
	sp.Set("gen_ai.usage.output_tokens", u.CompletionTokens)
	sp.Set("wisp.usage.cached_tokens", u.CachedTokens)
	if u.CostReported {
		sp.Set("wisp.usage.cost_usd", u.Cost)
	}
	if price.Known {
		cached := min(max(u.CachedTokens, 0), u.PromptTokens)
		cachedRate := price.CachedInput
		if cachedRate == 0 {
			cachedRate = price.Input
		}
		sp.Set("wisp.price", price.String())
		sp.Set("wisp.cost.input_usd", float64(u.PromptTokens-cached)*price.Input/1e6)
		sp.Set("wisp.cost.cached_input_usd", float64(cached)*cachedRate/1e6)
		sp.Set("wisp.cost.output_usd", float64(u.CompletionTokens)*price.Output/1e6)
		sp.Set("wisp.cost.list_usd", price.Cost(u.PromptTokens, u.CachedTokens, u.CompletionTokens))
	}
	switch {
	case u.CostReported:
		sp.Set("wisp.cost.usd", u.Cost)
		sp.Set("wisp.cost.source", "billed")
	case price.Known:
		sp.Set("wisp.cost.usd", price.Cost(u.PromptTokens, u.CachedTokens, u.CompletionTokens))
		sp.Set("wisp.cost.source", "list price")
	default:
		sp.Set("wisp.cost.source", "unknown")
	}
}
