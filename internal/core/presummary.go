package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tokencount"
)

// Precomputed summaries and rehydration: a summary prepared in the
// background, so a compaction needn't wait for one, and the files most
// recently changed re-attached to it, so the model needn't read them again.
const (
	presummarizePct = 50 // start a background summary past this share of the budget

	rehydrateFiles     = 3      // recently modified files re-attached after a compaction
	rehydratePct       = 10     // of the budget, at most
	rehydrateMinWindow = 32_768 // below this, the tail needs the room more
)

// presummary is a summary generated in the background at the end of a
// turn, while the backend's cache still holds the prefix and the GPU would
// otherwise sit idle, so the next compaction needn't wait for one.
type presummary struct {
	base   *Compaction // l.Compacted when it started: the summary it updates
	cut    int
	cancel context.CancelFunc
	done   chan struct{} // closed once summary and err are set

	summary string
	usage   model.Usage // of its requests, counted when the loop drops it
	err     error
}

// presummarize starts a background summary when history is past
// presummarizePct of the budget and no usable one exists. The request is
// built here; the goroutine only streams it, so it shares no loop state.
func (l *Loop) presummarize() {
	budget := l.historyBudget()
	if !l.AutoCompact || !l.Presummarize || budget == 0 || l.projectedHistory() <= budget*presummarizePct/100 {
		return
	}
	if l.pre != nil && l.usable(l.pre) {
		return
	}
	l.StopPresummary()
	cut := l.cutPoint(l.tailTokens(), 2*l.summaryCap())
	if cut == 0 {
		return
	}
	req := l.buildSummaryRequest(cut, "")
	// Masking writes into History's tool calls while this request streams.
	for i := range req.Messages {
		req.Messages[i].ToolCalls = slices.Clone(req.Messages[i].ToolCalls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	pre := &presummary{base: l.Compacted, cut: cut, cancel: cancel, done: make(chan struct{})}
	provider, spans, price := l.Provider, l.Spans, l.Price
	go func() {
		defer close(pre.done)
		pre.summary, pre.usage, pre.err = streamSummary(ctx, provider, spans, price, req)
	}()
	l.pre = pre
}

// usable reports whether pre still fits History: made against the current
// summary, with a tail since its cut that is still short. A failed one is
// usable until it finishes, since that isn't known yet.
func (l *Loop) usable(pre *presummary) bool {
	select {
	case <-pre.done:
		if pre.err != nil || strings.TrimSpace(pre.summary) == "" {
			return false
		}
	default:
	}
	if pre.base != l.Compacted || pre.cut <= l.keptFrom() || pre.cut >= len(l.History) {
		return false
	}
	tail := 0.0
	for _, msg := range l.History[pre.cut:] {
		tail += float64(messageTokens(msg)) * l.ratio()
	}
	return tail <= 2*float64(l.tailTokens())
}

// yieldPresummary runs at the start of a turn. A background summary still
// generating is cancelled, since on a single-slot server it would hold up
// the turn's own requests, unless the turn may summarize at once (past
// the mask trigger, see Fit) and so waits for it. A finished one is kept for later.
func (l *Loop) yieldPresummary(input string) {
	pre := l.pre
	if pre == nil {
		return
	}
	select {
	case <-pre.done:
		return
	default:
	}
	budget := l.historyBudget()
	needed := budget > 0 && l.projectedHistory()+int(float64(tokencount.Count(input))*l.ratio()) > budget*maskTriggerPct/100
	if !needed || !l.usable(pre) {
		l.StopPresummary()
	}
}

// StopPresummary cancels a background summary and waits for it to end.
// Frontends call it before dropping the loop.
func (l *Loop) StopPresummary() {
	if l.pre != nil {
		l.pre.cancel()
		<-l.pre.done
		l.addUsage(l.pre.usage) // spent even when unused
		l.pre = nil
	}
}

// FileSnapshot is a file's content re-attached to a summary, with line
// numbers as the read tool shows them.
type FileSnapshot struct {
	Path    string
	Content string
}

// rehydrate reads the files most recently modified before the cut, which
// the next steps most likely need, so the model needn't read them again
// after a compaction. It keeps to rehydratePct of the budget, skips a file
// over half of that, and is off for small windows.
func (l *Loop) rehydrate(lg Ledger) []FileSnapshot {
	if l.ContextWindow < rehydrateMinWindow {
		return nil
	}
	left := float64(l.historyBudget() * rehydratePct / 100)
	var files []FileSnapshot
	for i := len(lg.Modified) - 1; i >= 0 && len(files) < rehydrateFiles; i-- {
		data, err := os.ReadFile(lg.Modified[i].Path)
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		var b strings.Builder
		for n, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if n > 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "%6d\t%s", n+1, line)
		}
		cost := float64(tokencount.Count(b.String())) * l.ratio()
		if cost > float64(l.historyBudget()*rehydratePct/100)/2 || cost > left {
			continue
		}
		left -= cost
		files = append(files, FileSnapshot{Path: lg.Modified[i].Path, Content: b.String()})
	}
	return files
}
