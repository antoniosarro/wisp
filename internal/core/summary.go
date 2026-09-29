package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/prompt"
	"github.com/antoniosarro/wisp/internal/textfmt"
	"github.com/antoniosarro/wisp/internal/tokencount"
)

// The summary request: what the next step would send, plus the compaction
// instruction (prompt.Compact), so the backend's prefix cache covers all
// but the instruction.

// summarize asks the model for the summary of History before cut. The
// request is what the next step would send plus one instruction, so the
// backend's prefix cache covers all but the instruction. When the history
// no longer fits, everything outside the latest step is masked first.
func (l *Loop) summarize(ctx context.Context, cut int, focus string) (string, error) {
	text, err := l.summaryRequest(ctx, cut, focus)
	if errors.Is(err, model.ErrContextOverflow) && l.mask(0, false, 0) {
		text, err = l.summaryRequest(ctx, cut, focus)
	}
	return text, err
}

// summaryRequest sends the summary request and counts its usage.
func (l *Loop) summaryRequest(ctx context.Context, cut int, focus string) (string, error) {
	text, usage, err := streamSummary(ctx, l.Provider, l.buildSummaryRequest(cut, focus))
	l.addUsage(usage)
	return text, err
}

// buildSummaryRequest is the request for the summary of History before cut.
func (l *Loop) buildSummaryRequest(cut int, focus string) model.Request {
	req := model.Request{
		Messages:  append(l.Messages(), model.Message{Role: model.RoleUser, Content: l.summaryInstruction(cut, focus)}),
		Tools:     l.requestTools(),
		MaxTokens: l.summaryCap(),
		// Thinking tokens would come out of the cap and delay the summary.
		NoReasoning: true,
	}
	if req.Tools != nil {
		req.ToolChoice = "none"
	}
	return req
}

// errOnlyReasoning is a summary response that spent its whole output cap
// reasoning, leaving no summary.
var errOnlyReasoning = errors.New("the model spent the summary's length limit reasoning")

// streamSummary sends a summary request and returns the summary. When the
// backend rejects the request for asking for no reasoning (some endpoints
// make it mandatory), or reasons anyway until the cap cuts it off, the
// request is sent once more with reasoning allowed but bounded, where the
// backend can bound it, and room for it on top of the summary's own cap.
// It touches no loop state, so it can run in the background; the usage of
// its requests is returned for the caller to count.
func streamSummary(ctx context.Context, provider model.Provider, req model.Request) (string, model.Usage, error) {
	summaryCap := req.MaxTokens
	text, usage, err := streamSummaryOnce(ctx, provider, req)
	if err != nil && ctx.Err() == nil && !errors.Is(err, model.ErrContextOverflow) && (req.NoReasoning || errors.Is(err, errOnlyReasoning)) {
		req.NoReasoning = false
		req.MaxTokens = max(4*summaryCap, 8192)
		req.ReasoningTokens = req.MaxTokens - summaryCap
		var again model.Usage
		text, again, err = streamSummaryOnce(ctx, provider, req)
		usage = sumUsage(usage, again)
	}
	// The retry's cap leaves room for reasoning a backend may not spend,
	// or may send as text; the summary itself stays within its own.
	return capSummary(text, summaryCap), usage, err
}

// sumUsage adds up the usage of two requests.
func sumUsage(a, b model.Usage) model.Usage {
	a.PromptTokens += b.PromptTokens
	a.CompletionTokens += b.CompletionTokens
	a.TotalTokens += b.TotalTokens
	a.CachedTokens += b.CachedTokens
	a.Cost += b.Cost
	a.CostReported = a.CostReported || b.CostReported
	return a
}

// capSummary cuts a summary longer than limit tokens at the last whole
// line that fits.
func capSummary(text string, limit int) string {
	if limit <= 0 || tokencount.Count(text) <= limit {
		return text
	}
	var b strings.Builder
	used := 0
	for line := range strings.Lines(text) {
		n := tokencount.Count(line)
		if used+n > limit {
			break
		}
		b.WriteString(line)
		used += n
	}
	return strings.TrimRight(b.String(), "\n") + "\n(summary cut at its length limit)"
}

// streamSummaryOnce sends one summary request and returns its text. A
// response cut off at the cap with only reasoning in it is
// errOnlyReasoning; one cut off mid-summary keeps what it has, marked.
func streamSummaryOnce(ctx context.Context, provider model.Provider, req model.Request) (_ string, usage model.Usage, err error) {
	events, err := provider.Stream(ctx, req)
	if err != nil {
		return "", usage, fmt.Errorf("starting summary: %w", err)
	}
	var text strings.Builder
	truncated, reasoned := false, false
	for e := range events {
		switch e.Kind {
		case model.EventTextDelta:
			text.WriteString(e.Text)
		case model.EventReasoningDelta:
			reasoned = true
		case model.EventReclassify:
			text.Reset()
			reasoned = true
		case model.EventDone:
			truncated = e.Truncated
			if e.Usage != nil {
				usage = *e.Usage
			}
		case model.EventError:
			return "", usage, fmt.Errorf("summary stream: %w", e.Err)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", usage, err
	}
	summary := strings.TrimSpace(text.String())
	if truncated && summary == "" && reasoned {
		return "", usage, errOnlyReasoning
	}
	if truncated && summary != "" {
		summary += "\n(summary cut off at its length limit)"
	}
	return summary, usage, nil
}

// summaryInstruction is prompt.Compact plus where the cut falls, whether
// an earlier summary is to be updated, and the user's focus.
func (l *Loop) summaryInstruction(cut int, focus string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(prompt.Compact))
	kept := l.History[cut]
	where := "the message"
	switch {
	case kept.Role == model.RoleUser:
		where = fmt.Sprintf("the user's message that begins %q", firstLine(kept.Content))
	case strings.TrimSpace(kept.Content) != "":
		where = fmt.Sprintf("your message that begins %q", firstLine(kept.Content))
	case len(kept.ToolCalls) > 0:
		c := kept.ToolCalls[0]
		where = fmt.Sprintf("your %s call", strings.TrimSpace(c.Name+" "+callSubject(c.Args)))
	}
	fmt.Fprintf(&b, "\n\nEverything before %s is replaced by your summary. That message and everything after it stay verbatim, so leave them out of the summary.", where)
	if kept.Role == model.RoleAssistant {
		b.WriteString(" The cut falls inside the current turn: add a heading \"Current turn so far\" saying what the user asked in this turn and what you have done for it before the cut.")
	}
	if l.Compacted != nil {
		b.WriteString("\n\nThe conversation starts with an earlier summary, marked " + summaryHeader + ". Return that summary updated with what happened after it: keep every heading and every point still true, add new points, mark resolved ones as resolved, and remove only what later messages contradict.")
	}
	if focus = strings.TrimSpace(focus); focus != "" {
		fmt.Fprintf(&b, "\n\nThe user asks that the summary keep this in detail: %s", focus)
	}
	return b.String()
}

// firstLine names a message after its first line, cut to 80 characters.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return textfmt.CutRunes(line, 80)
}
