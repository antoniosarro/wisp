package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// applyLayout recomputes component sizes from the terminal size and the
// input's height.
func (m *Model) applyLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.input.SetWidth(max(1, m.width-6))
	m.input.SetHeight(min(5, max(1, m.height-5), max(1, m.input.LineCount()))) // leave room for a 1-line chat box
	boxHeight := max(0, m.height-lipgloss.Height(m.footer())-2)                // chat box border
	if !m.ready {
		m.viewport = viewport.New(m.chatWidth(), boxHeight)
		m.ready = true
	} else {
		m.viewport.Width = m.chatWidth()
		m.viewport.Height = boxHeight
	}
	m.syncViewport()
}

// chatWidth is the transcript's content width inside the chat box.
func (m *Model) chatWidth() int {
	return max(1, m.width-6)
}

// syncViewport re-renders the transcript into the viewport.
func (m *Model) syncViewport() {
	if !m.ready {
		return
	}
	m.dirty = false
	m.viewport.SetContent(m.render())
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

// lineRange is a half-open range of transcript lines.
type lineRange struct{ start, end int }

// render builds the transcript, padded toward the bottom of the viewport
// so a short chat sits next to the input. Finished blocks come from their
// cached render. It records each block's line range, to scroll to it.
func (m *Model) render() string {
	w := m.chatWidth()
	var chat strings.Builder
	lines := 0
	m.blockLines = m.blockLines[:0]
	for i := range m.blocks {
		b := &m.blocks[i]
		if b.cached == "" || b.cachedWidth != w || b.active() {
			live := m.inTurn && i == len(m.blocks)-1
			// Hardwrap as well: a word longer than the line would overflow the box.
			b.cached = ansi.Hardwrap(m.renderBlock(b, w, live), w, true)
			b.cachedWidth = w
		}
		part := b.cached
		if i == m.selectedBlock {
			part = markGutter(part, styleSelectedBar)
		}
		if i > 0 {
			chat.WriteString("\n\n") // one blank line between blocks
			lines++
		}
		n := strings.Count(part, "\n") + 1
		m.blockLines = append(m.blockLines, lineRange{lines, lines + n})
		chat.WriteString(part)
		lines += n
	}
	if len(m.blocks) == 0 {
		return ""
	}
	// The padding shifts the blocks down, but only when they all fit, when
	// there is nothing to scroll to: blockLines leaves it out.
	return strings.Repeat("\n", max(0, m.viewport.Height-lines)) + chat.String()
}

// markGutter draws a bar in the left margin of each line.
func markGutter(part string, style lipgloss.Style) string {
	bar := style.Render("▌")
	lines := strings.Split(part, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, " ") {
			lines[i] = bar + l[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// renderBlock renders b; live marks the block the model is still writing.
func (m *Model) renderBlock(b *block, w int, live bool) string {
	b = b.sanitized()
	switch b.kind {
	case blockUser:
		return renderUserPrompt(b.text, w)
	case blockAnswer:
		if live {
			return renderLiveAnswer(b.text, w)
		}
		return renderAnswer(b.text, w)
	case blockNotice:
		return renderNotice(b.text, w)
	case blockTurnError:
		return renderTurnError(b.text, w)
	case blockReasoning:
		return m.renderReasoningBlock(b, w)
	case blockToolCall:
		return m.renderToolCallBlock(b, w)
	}
	return ""
}

// renderReasoningBlock shows reasoning as a live timer while it streams,
// then as a one-line summary, or in full when expanded.
func (m *Model) renderReasoningBlock(b *block, w int) string {
	switch {
	case b.expanded:
		suffix := ""
		if !b.reasoningDone {
			suffix = " " + m.spin.View()
		}
		return styleChatMargin.Render(renderReasoningFull(b.reasoningText, suffix, w))
	case !b.reasoningDone:
		elapsed := formatDuration(elapsedSince(b.reasoningStart))
		return styleChatMargin.Render(m.spin.View() + " " + styleReasoning.Render("Thinking… "+elapsed) + styleDim.Render(" · esc to interrupt"))
	default:
		return styleChatMargin.Render(renderReasoningSummary(b.reasoningText, b.reasoningTime))
	}
}

// renderToolCallBlock aligns tool cards with the answer body column.
func (m *Model) renderToolCallBlock(b *block, w int) string {
	w = max(1, w-chatContentLeft)
	var card string
	switch b.toolStatus {
	case toolRunning:
		card = renderToolRunning(m.spin.View(), b.toolName, b.toolArgs, elapsedSince(b.toolStart), w)
	case toolDenied:
		card = renderToolDenied(b.toolName, b.toolArgs, w)
	default:
		failed := b.toolStatus == toolFailed
		card = renderToolResult(b.toolName, b.toolArgs, b.toolResult, b.toolTime, w, failed, b.expanded)
		if b.expanded {
			card += "\n\n" + renderToolOutput(b.toolName, b.toolArgs, b.toolResult, failed, w)
		}
	}
	return lipgloss.NewStyle().PaddingLeft(chatContentLeft).Render(card)
}

// View draws the chat box over the input.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "loading...\n"
	}
	if m.width < 20 || m.height < 8 {
		return fitView("Terminal too small; resize to 20×8", m.width, m.height)
	}
	return fitView(m.chatBox(max(1, m.width-2))+"\n"+m.footer(), m.width, m.height)
}

// fitView clips s to the terminal, so an oversized frame can't scroll it.
func fitView(s string, w, h int) string {
	return lipgloss.NewStyle().MaxWidth(max(1, w)).MaxHeight(max(1, h)).Render(s)
}

// footer is what sits below the chat box: the input.
func (m *Model) footer() string {
	return renderInputBox(m.input.View(), m.width)
}

// chatBox frames the transcript, with any notice in its bottom border.
func (m *Model) chatBox(width int) string {
	box := styleChatBox.Width(width).Render(m.viewport.View())
	label := m.notice
	switch {
	case label != "":
	case !m.autoScroll:
		label = "↓ ctrl+end for latest"
	}
	return withBorderLabel(box, label)
}

// withBorderLabel writes label into the bottom border of a rounded box.
func withBorderLabel(box, label string) string {
	if label == "" {
		return box
	}
	lines := strings.Split(box, "\n")
	last := len(lines) - 1
	lines[last] = borderLine("╰", "╯", styleDim.Render(label), lipgloss.Width(lines[last]), styleBorderLine)
	return strings.Join(lines, "\n")
}

// borderLine draws a width-wide horizontal border with label embedded.
func borderLine(start, end, label string, width int, border lipgloss.Style) string {
	label = ansi.Truncate(label, max(0, width-6), "…")
	used := 5 + ansi.StringWidth(label) // "╰─ " + label + " " + end
	return border.Render(start+"─ ") + label + border.Render(" "+strings.Repeat("─", max(0, width-used))) + border.Render(end)
}
