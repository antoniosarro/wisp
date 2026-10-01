package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
)

// debugBarWidth is the width of the context bar.
const debugBarWidth = 14

// renderDebugPanel shows provider-reported token usage with an estimated
// breakdown by content type, cost, and timing. height <= 0 means unbounded.
func renderDebugPanel(s core.StepStats, opts Options, cost costSummary, agentTokens, height, width int) string {
	inner := max(10, width-4) // border + padding
	var b strings.Builder
	dim := func(v any) string { return styleDim.Render(fmt.Sprint(v)) }
	row := func(label string, last, session any) {
		fmt.Fprintf(&b, "%-11s %s %s\n", label, styleDim.Render(fmt.Sprintf("%-9v", last)), dim(session))
	}

	fmt.Fprintln(&b, styleSplashHead.Render("Debug"))
	// Both names come from the endpoint.
	fmt.Fprintf(&b, "● %s\n", dim(sanitize(opts.Model.ID)))
	if cost.Provider != "" {
		fmt.Fprintf(&b, "  %s\n", dim("via "+sanitize(cost.Provider)))
	}
	fmt.Fprintln(&b)

	fmt.Fprintf(&b, "%s %s\n", styleSplashHead.Render(fmt.Sprintf("%-11s", "Tokens")), dim(fmt.Sprintf("%-9s %s", "last", "session")))
	// Estimated parts of the last request, indented under what they split.
	part := func(label string, tokens int) {
		if tokens > 0 {
			fmt.Fprintf(&b, "  %-9s %s\n", label, dim(fmt.Sprintf("~%d", tokens)))
		}
	}
	promptParts := func() {
		part("system", s.SystemTokensEst)
		part("history", s.HistoryTokensEst)
		part("tool defs", s.ToolDefTokensEst)
	}
	completionParts := func() {
		part("reasoning", s.ReasoningTokensEst)
		part("answer", s.AnswerTokensEst)
		part("calls", s.ToolCallTokensEst)
	}
	if s.UsageAvailable {
		row("prompt", s.PromptTokens, s.SessionPromptTokens)
		promptParts()
		if s.SessionCachedTokens > 0 {
			last, _ := core.CacheHitRate(s.CachedTokens, s.PromptTokens)
			session, _ := core.CacheHitRate(s.SessionCachedTokens, s.SessionPromptTokens)
			row("cache hit", fmt.Sprintf("%.0f%%", last), fmt.Sprintf("%.1f%%", session))
		}
		row("completion", s.CompletionTokens, s.SessionCompletionTokens)
		completionParts()
	} else {
		fmt.Fprintln(&b, dim("provider usage unavailable"))
		promptParts()
		completionParts()
	}
	if agentTokens > 0 {
		row("agents", "", agentTokens)
	}
	fmt.Fprintln(&b)
	writeContext(&b, s.Context, dim)

	writeCost(&b, opts.Model, cost, inner, row)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, styleSplashHead.Render("Performance"))
	fmt.Fprintln(&b, dim(fmt.Sprintf("%.1f tok/s · %s · %d req", s.TokensPerSec, s.Duration.Round(10*time.Millisecond), s.RequestCount)))

	return renderSidePanel(strings.TrimRight(b.String(), "\n"), height, width)
}

// costSummary is the spend of a session. Each request is costed when its
// usage arrives: at what the endpoint billed when it reports that
// (OpenRouter, at whichever provider served it), else at the list price of
// the model that served it. Switching models or providers mid-session
// keeps earlier requests at their own cost.
type costSummary struct {
	Last       float64 // the latest request
	LastPriced bool
	Billed     int    // requests costed at the endpoint's bill rather than a list price
	Estimated  int    // requests costed at a list price
	Provider   string // who served the latest request, when reported
	Session    float64
	Agents     float64 // sub-agents
	AgentsEst  bool    // some agent spend is at their model's list price, not billed
	Unpriced   int     // requests with usage but no known cost
}

// add costs one request's usage.
func (c *costSummary) add(s core.StepStats, info model.Info) {
	if !s.UsageAvailable {
		return
	}
	c.Provider = s.Provider
	c.Last, c.LastPriced = 0, true
	switch {
	case s.CostReported:
		c.Last = s.Cost
		c.Billed++
	case info.Price.Known:
		c.Last = info.Price.Cost(s.PromptTokens, s.CachedTokens, s.CompletionTokens)
		c.Estimated++
	case !info.Local:
		c.Unpriced++
		fallthrough
	default:
		c.LastPriced = false
	}
	c.Session += c.Last
}

// costSummary adds the sub-agents' spend to the main chat's. Each run
// prices its own requests: what its endpoint billed, else its own model's
// list price with cached tokens at the cached rate.
func (m *Model) costSummary() costSummary {
	c := m.cost
	for _, r := range m.agentRuns {
		c.Agents += r.Cost
		c.AgentsEst = c.AgentsEst || r.CostEstimated
	}
	return c
}

// writeCost renders the cost section: spend so far and where the figures
// come from, or why there are none.
func writeCost(b *strings.Builder, info model.Info, c costSummary, inner int, row func(label string, last, session any)) {
	note := func(s string) { fmt.Fprintln(b, styleDim.Render(ansi.Wrap(s, inner, " "))) }
	head := "Cost"
	switch {
	case c.Billed > 0 && c.Estimated == 0 && !c.AgentsEst:
		head += " (billed)"
	case c.Billed == 0:
		head += " (est.)"
	}
	fmt.Fprintln(b, styleSplashHead.Render(head))
	switch {
	case !info.Price.Known && c.Session == 0 && c.Agents == 0 && info.Local:
		note("local endpoint: no API cost")
		return
	case !info.Price.Known && c.Session == 0 && c.Agents == 0 && c.Billed == 0:
		note("price unknown")
		note("--price IN,OUT sets it ($/1M)")
		return
	}
	last := "-"
	if c.LastPriced {
		last = usd(c.Last)
	}
	row("spent", last, usd(c.Session))
	if c.Agents > 0 {
		agents := usd(c.Agents)
		if c.AgentsEst {
			agents += " *"
		}
		row("agents", "", agents)
	}
	if c.Billed > 0 && c.Estimated > 0 {
		note(fmt.Sprintf("%d billed, %d at list price", c.Billed, c.Estimated))
	}
	if info.Price.Known && (c.Estimated > 0 || c.AgentsEst || c.Billed == 0) {
		note(info.Price.String())
	}
	if c.AgentsEst {
		note("* list price, as if uncached")
	}
	if c.Unpriced > 0 {
		note(fmt.Sprintf("%d request(s) had no known price", c.Unpriced))
	}
}

// usd formats dollars with the precision small amounts need.
func usd(v float64) string {
	switch {
	case v == 0:
		return "$0"
	case v < 0.01:
		return fmt.Sprintf("$%.4f", v)
	case v < 1:
		return fmt.Sprintf("$%.3f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// writeContext shows the context budget: what history uses against its
// thresholds, and what compaction has done.
func writeContext(b *strings.Builder, c core.ContextStats, dim func(any) string) {
	if c.Window == 0 && c.History == 0 {
		return
	}
	row := func(label, value string) { fmt.Fprintf(b, "%-11s %s\n", label, dim(value)) }
	fmt.Fprintln(b, styleSplashHead.Render("Context"))
	if c.Window == 0 {
		row("window", "unknown")
	} else {
		row("window", shortTokens(c.Window))
		row("  fixed", shortTokens(c.Fixed))
		row("  history", shortTokens(c.Budget)+" budget")
		fmt.Fprintf(b, "%-11s %s %s\n", "used", debugBar(c.History, c.Budget, debugBarWidth), dim(fmt.Sprintf("%.0f%%", float64(c.History)/float64(max(1, c.Budget))*100)))
		row("mask at", shortTokens(c.MaskTrigger()))
		row("summarize", "at "+shortTokens(c.SummarizeTrigger()))
	}
	if c.Ratio != 0 && c.Ratio != 1 {
		row("estimates", fmt.Sprintf("×%.2f", c.Ratio))
	}
	if c.MaskedResults > 0 {
		row("masked", fmt.Sprintf("%d outputs", c.MaskedResults))
	}
	if c.MaskedCalls > 0 {
		row("  bodies", fmt.Sprint(c.MaskedCalls))
	}
	if c.Compactions > 0 {
		row("compacted", fmt.Sprintf("%d×", c.Compactions))
	}
	if c.Presummary != "" {
		row("presummary", c.Presummary)
	}
	fmt.Fprintln(b)
}

// debugBar renders value/total as a fixed-width bar.
func debugBar(value, total, width int) string {
	filled := min(width, max(0, value*width/max(1, total)))
	return styleAnswerPrefix.Render(strings.Repeat("█", filled)) + styleDim.Render(strings.Repeat("░", width-filled))
}
