package tui

import (
	"context"
	"fmt"

	"github.com/antoniosarro/wisp/internal/core"
)

// startCompact runs /compact like a turn: Esc cancels it, and the input
// waits until it ends.
func (m *Model) startCompact(focus string) {
	switch {
	case m.inTurn:
		m.notify("Wait for the current turn to finish, then /compact.")
		return
	case m.opts.Model.ID == "":
		m.notify(msgChooseModel)
		return
	}
	m.inTurn = true
	ctx, cancel := context.WithCancel(m.ctx)
	m.turnCancel = cancel
	m.turnDone = RunCompact(ctx, m.loop, focus, m.send)
}

// applyCompaction shows a compaction starting, then fills the block in
// when it ends.
func (m *Model) applyCompaction(e core.CompactEvent) {
	if !e.Done {
		m.appendBlock(block{kind: blockCompaction})
		return
	}
	m.stats.Context = e.Context
	if m.debugOpen {
		m.refreshDebug()
	}
	b := compactionBlock(e.Compaction, e.Compaction.Text())
	if e.Err != nil {
		b.text += fmt.Sprintf(" · the model's summary failed (%v); kept the recorded facts only", e.Err)
	}
	if last := m.blocks.last(blockCompaction); last != nil && last.text == "" {
		*last = b
		return
	}
	m.appendBlock(b)
}

// compactionBlock marks where the model's view begins; expanded, it shows
// the summary that stands in for everything above it.
func compactionBlock(c core.Compaction, summary string) block {
	text := "Compacted context"
	if c.TokensBefore > 0 {
		text += fmt.Sprintf(": %s → %s tokens", shortTokens(c.TokensBefore), shortTokens(c.TokensAfter))
	}
	if c.Precomputed {
		text += " (summary prepared in the background)"
	}
	return block{kind: blockCompaction, text: text + " · the model sees a summary of everything above", detail: summary}
}

// renderCompactionBlock shows a compaction running, or what it did and,
// expanded, the summary.
func (m *Model) renderCompactionBlock(b *block, w int) string {
	if b.text == "" {
		return styleChatMargin.Render(m.spin.View() + " " + styleDim.Render("Compacting context…") + styleDim.Render(" · esc to interrupt"))
	}
	out := renderNotice(b.text, w)
	if b.expanded {
		out += "\n\n" + renderNotice(b.detail, w)
	}
	return out
}

// shortTokens formats a token count as 950 or 12.3K.
func shortTokens(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fK", float64(n)/1000)
}
