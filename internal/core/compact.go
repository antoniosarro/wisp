package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tokencount"
)

// Summarization, the step after masking: History before a cut is replaced,
// in what requests send, by a summary the model writes (summary.go) plus
// the ledger (ledger.go). History itself keeps every message.

const (
	summarizePct = 85 // summarize past this share of the budget

	summaryHeader = "[wisp summary of the earlier conversation]"
	// recordsNote tells the model the tagged sections are exact, since
	// small models otherwise answer from the recent messages alone.
	recordsNote = "The tagged sections below the summary are exact records wisp took from the conversation: every file changed or read, every command run, the task list, and the user's messages. File contents are not repeated here: read a file again when you need what it says."
	// summaryFailed stands in for the model's part of a summary when the
	// request for it failed; the ledger still carries the facts.
	summaryFailed = "(The narrative summary could not be written. The sections below were recorded from the conversation.)"
)

// ErrNothingToCompact is returned when History is too short to leave
// anything to summarize after the kept tail.
var ErrNothingToCompact = errors.New("nothing to compact: the conversation fits in the part kept verbatim")

// Compaction replaces History[:FirstKept] in requests with a summary.
type Compaction struct {
	FirstKept    int    // index in History of the first message sent verbatim
	Summary      string // the model's sections
	Ledger       Ledger // facts from History[:FirstKept]
	TokensBefore int
	TokensAfter  int
	Precomputed  bool           // the summary was generated in the background, before it was needed
	Files        []FileSnapshot `json:",omitempty"` // recently modified files, as they were at compaction

	text string // the summary message as sent, rendered once so the prefix stays stable
}

// Compactor is implemented by stores that keep compactions, so a resumed
// session starts from its latest summary.
type Compactor interface {
	SaveCompaction(sessionID string, c Compaction) error
	// Compactions returns the session's compactions, oldest first.
	Compactions(sessionID string) ([]Compaction, error)
}

// CompactEvent reports a compaction's start (Done false) and end.
type CompactEvent struct {
	Done       bool
	Compaction Compaction   // on Done
	Err        error        // on Done: why the model's summary is missing, if it is
	Context    ContextStats // on Done: the budget after it
}

// keptFrom is the index of the first message requests send verbatim.
func (l *Loop) keptFrom() int {
	if l.Compacted == nil {
		return 0
	}
	return l.Compacted.FirstKept
}

// LoadCompaction restores the session's compactions from the store, if it
// keeps them; requests then start from the latest summary. Compactions
// past the end of History, from a history that lost messages, are
// dropped. Call it after the loop's window is set: summaries are rendered
// for it.
func (l *Loop) LoadCompaction() error {
	store, ok := l.Store.(Compactor)
	if !ok {
		return nil
	}
	all, err := store.Compactions(l.SessionID)
	if err != nil {
		return err
	}
	l.Compactions = nil
	for _, c := range all {
		if c.FirstKept > 0 && c.FirstKept < len(l.History) {
			c.text = c.render(l.userMessageTokens())
			l.Compactions = append(l.Compactions, c)
		}
	}
	if n := len(l.Compactions); n > 0 {
		c := l.Compactions[n-1]
		l.Compacted, l.tokens.counted = &c, 0
	}
	return nil
}

// Text is the summary message as requests send it.
func (c *Compaction) Text() string { return c.text }

// render writes the summary message: the header, the model's sections, and
// the ledger.
func (c *Compaction) render(userTokens int) string {
	var b strings.Builder
	b.WriteString(summaryHeader + "\n\n")
	b.WriteString(strings.TrimSpace(c.Summary))
	b.WriteString("\n\n")
	b.WriteString(recordsNote)
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(c.Ledger.Render(userTokens)))
	for _, f := range c.Files {
		fmt.Fprintf(&b, "\n<file path=%q>\n%s\n</file>", f.Path, f.Content)
	}
	return b.String()
}

// Compact summarizes History up to a cut, keeping the recent tail
// verbatim; focus, if set, says what the summary should keep in detail. It
// fails only when there is nothing to summarize or ctx ends: when the
// summary request fails, the ledger alone stands in for the summary.
func (l *Loop) Compact(ctx context.Context, focus string) error {
	pre := l.pre
	if focus != "" || pre != nil && !l.usable(pre) {
		l.StopPresummary()
		pre = nil
	}
	cut := l.cutPoint(l.tailTokens(), 2*l.summaryCap())
	if pre != nil {
		cut = pre.cut
	}
	if cut == 0 {
		return ErrNothingToCompact
	}
	before := l.projectedHistory() + l.fixedTokens()
	if l.OnCompact != nil {
		l.OnCompact(CompactEvent{})
	}

	var summary string
	var err error
	precomputed := false
	if pre != nil {
		select {
		case <-pre.done:
		case <-ctx.Done():
			return ctx.Err() // it keeps running, for the next attempt
		}
		l.pre = nil
		l.addUsage(pre.usage)
		summary, err = pre.summary, pre.err
		precomputed = err == nil && strings.TrimSpace(summary) != ""
	}
	if !precomputed {
		summary, err = l.summarize(ctx, cut, focus)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil && strings.TrimSpace(summary) == "" {
		err = errors.New("the model returned an empty summary")
	}
	if err != nil {
		summary = summaryFailed
	}

	var lg Ledger
	from := 0
	if prev := l.Compacted; prev != nil {
		lg, from = prev.Ledger.clone(), prev.FirstKept
	}
	lg.Update(l.History[from:cut])
	for _, msg := range l.History[from:cut] {
		for _, call := range msg.ToolCalls {
			delete(l.ran, callKey(call)) // its result is only summarized now: no repeat
		}
	}
	c := &Compaction{FirstKept: cut, Summary: summary, Ledger: lg, TokensBefore: before, Precomputed: precomputed, Files: l.rehydrate(lg)}
	c.text = c.render(l.userMessageTokens())
	l.Compacted, l.tokens.counted = c, 0
	c.TokensAfter = l.projectedHistory() + l.fixedTokens()
	l.Compactions = append(l.Compactions, *c)

	if store, ok := l.Store.(Compactor); ok {
		// Losing the record only means compacting again after a resume.
		_ = store.SaveCompaction(l.SessionID, *c)
	}
	if l.OnCompact != nil {
		l.OnCompact(CompactEvent{Done: true, Compaction: *c, Err: err, Context: l.contextStats()})
	}
	return nil
}

// clone deep-copies a ledger, whose Update edits its slices in place.
func (lg Ledger) clone() Ledger {
	var c Ledger
	b, _ := json.Marshal(lg)
	_ = json.Unmarshal(b, &c)
	return c
}

// cutPoint picks the first message of the tail kept verbatim: the user
// message starting the turn that holds the last tail tokens of History,
// or, when that turn alone is over twice the tail (a long turn), the
// assistant message that starts a step inside it. A tool call is never
// separated from its results. It returns 0 when there is nothing before
// the cut to summarize, or less than least tokens: a summary would free
// little or nothing.
func (l *Loop) cutPoint(tail, least int) int {
	cut := l.tailStart(tail)
	summarized := 0.0
	for _, msg := range l.History[l.keptFrom():cut] {
		summarized += float64(messageTokens(msg)) * l.ratio()
	}
	if summarized < float64(least) {
		return 0
	}
	return cut
}

func (l *Loop) tailStart(tail int) int {
	// A tiny budget rounds the tail to 0 tokens; keep at least the last
	// message, so the walk below starts inside History.
	tail = max(tail, 1)
	from := l.keptFrom()
	i, used := len(l.History), 0.0
	for i > from+1 && used < float64(tail) {
		i--
		used += float64(messageTokens(l.History[i])) * l.ratio()
	}
	if used < float64(tail) {
		return 0
	}
	for u, turn := i, used; u > from; u-- {
		if u < i {
			turn += float64(messageTokens(l.History[u])) * l.ratio()
		}
		if turn > 2*float64(tail) {
			break
		}
		if msg := l.History[u]; msg.Role == model.RoleUser && !strings.HasPrefix(msg.Content, ReminderPrefix) {
			return u
		}
	}
	for a := i; a > from; a-- {
		if l.History[a].Role == model.RoleAssistant {
			return a
		}
	}
	return 0
}

// tailTokens is how much recent history a summary keeps verbatim.
func (l *Loop) tailTokens() int {
	if budget := l.historyBudget(); budget > 0 {
		return min(20_000, budget*25/100)
	}
	return 20_000
}

// summaryCap is the summary request's output-token cap: short, since the
// ledger carries the lists and generation is the slow part.
func (l *Loop) summaryCap() int {
	if budget := l.historyBudget(); budget > 0 {
		return min(max(budget*8/100, 1024), 3072)
	}
	return 2048
}

// userMessageTokens is how many tokens of user messages a summary keeps.
func (l *Loop) userMessageTokens() int {
	if budget := l.historyBudget(); budget > 0 {
		return min(budget*15/100, maxUserTokens)
	}
	return maxUserTokens
}

// summaryTokens estimates the summary message.
func (l *Loop) summaryTokens() int {
	if l.Compacted == nil {
		return 0
	}
	return messageOverhead + tokencount.Count(l.Compacted.text)
}
