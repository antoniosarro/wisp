package tui

import (
	"cmp"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/agent"
)

// applyLayout recomputes component sizes from the terminal size and
// whatever currently occupies the footer and side panel.
func (m *Model) applyLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.input.SetWidth(max(1, m.width-6))
	m.input.SetHeight(min(5, max(1, m.height-5), max(1, m.input.LineCount()))) // leave room for a 1-line chat box
	if len(m.pending) > 0 {
		req := m.pending[0]
		m.approval.Width = max(1, m.width-4)
		// A write's details read the file and diff it line by line: they
		// are rebuilt only when the request or the size changes.
		if key := (approvalKey{req.Reply, m.width, m.height}); key != m.approvalKey {
			details := permissionDetails(req.Name, req.Args, m.opts.WorkDir)
			if req.Agent != "" {
				details = styleSpinner.Render(req.Agent) + styleDim.Render(" ▸ ") + details
			}
			details = ansi.Hardwrap(details, m.approval.Width, true)
			m.approval.Height = max(1, min(14, m.height/2-4, lipgloss.Height(details)))
			m.approval.SetContent(details)
			m.approvalKey = key
		}
	}
	boxHeight := max(0, m.height-lipgloss.Height(m.footer())-2) // chat box border
	if !m.ready {
		m.viewport = viewport.New(m.chatWidth(), boxHeight)
		m.ready = true
	} else {
		m.viewport.Width = m.chatWidth()
		m.viewport.Height = boxHeight
	}
	// Full-screen debug takes the chat box's place, borders included.
	m.debug.Width, m.debug.Height = max(1, m.width), boxHeight+2
	if m.debugOpen {
		m.refreshDebug()
	}
	if m.overlayText != "" {
		m.overlay.Width, m.overlay.Height = max(1, m.chatWidth()), boxHeight
		m.overlay.SetContent(ansi.Hardwrap(renderNotice(m.overlayText, m.chatWidth()), m.chatWidth(), true))
	}
	m.syncViewport()
}

// approvalKey identifies what the approval viewport shows.
type approvalKey struct {
	reply         chan<- Answer
	width, height int
}

// refreshDebug re-renders the debug panel's full-screen view.
func (m *Model) refreshDebug() {
	m.debug.SetContent(renderDebugPanel(m.stats, m.opts, m.costSummary(), m.agentTokens(), 0, min(sidePanelWidth, m.debug.Width)))
}

// chatWidth is the transcript's content width inside the chat box.
func (m *Model) chatWidth() int {
	if side := m.sideWidth(); side > 0 {
		return max(20, m.width-side-6)
	}
	return max(1, m.width-6)
}

// syncViewport re-renders the transcript into the viewport.
func (m *Model) syncViewport() {
	if !m.ready {
		return
	}
	m.dirty = false
	m.content = m.render()
	m.refreshViewport()
}

// refreshViewport shows the last render, with any drag selection highlighted.
func (m *Model) refreshViewport() {
	content := m.content
	if m.sel.visible() {
		content = m.highlightSelection(content)
	}
	m.viewport.SetContent(content)
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

// lineRange is a half-open range of transcript lines.
type lineRange struct{ start, end int }

// render builds the transcript, padded toward the bottom of the viewport
// so a short chat sits next to the input. Finished blocks come from their
// cached render. It records each block's line range, to scroll to it and
// to find the block under the mouse.
func (m *Model) render() string {
	w := m.chatWidth()
	var chat strings.Builder
	lines := 0
	m.blockLines = m.blockLines[:0]
	shown := *m.shown()
	for i := range shown {
		b := &shown[i]
		if b.cached == "" || b.cachedWidth != w || b.active() {
			live := m.inTurn && i == len(shown)-1
			// Hardwrap as well: a word longer than the line would overflow the box.
			b.cached = ansi.Hardwrap(m.renderBlock(b, w, live), w, true)
			b.cachedWidth = w
		}
		part := b.cached
		switch i {
		case m.selectedBlock:
			part = markGutter(part, styleSelectedBar)
		case m.hoverBlock:
			part = markGutter(part, styleHoverBar)
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
	if len(shown) == 0 {
		return ""
	}
	pad := max(0, m.viewport.Height-lines)
	for i := range m.blockLines {
		m.blockLines[i].start += pad
		m.blockLines[i].end += pad
	}
	return strings.Repeat("\n", pad) + chat.String()
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
	case blockCompaction:
		return m.renderCompactionBlock(b, w)
	case blockContext:
		return renderContextBlock(*b.usage, w)
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
		activity := ""
		if run := m.agentRunFor(b.toolCallID); b.toolName == agent.ToolName && run != nil {
			activity = runActivity(*run)
		}
		card = renderToolRunning(m.spin.View(), b.toolName, b.toolArgs, activity, elapsedSince(b.toolStart), w)
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

// View draws the chat box, and any side panels, over the footer.
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
	var main string
	switch {
	case m.viewport.Height <= 0 && !m.debugFullscreen():
		// The footer (a tall approval box) takes the whole screen; an
		// empty chat box would push its bottom off it.
		return fitView(m.footer(), m.width, m.height)
	case m.debugFullscreen():
		main = m.debug.View()
	case m.sideWidth() > 0:
		chat := m.chatBox(max(1, m.width-m.sideWidth()-2))
		main = lipgloss.JoinHorizontal(lipgloss.Top, chat, m.renderSideColumn(m.viewport.Height+2))
	default:
		main = m.chatBox(max(1, m.width-2))
	}
	return fitView(m.overlayPicker(main)+"\n"+m.footer(), m.width, m.height)
}

// fitView clips s to the terminal, so an oversized frame can't scroll it.
func fitView(s string, w, h int) string {
	return lipgloss.NewStyle().MaxWidth(max(1, w)).MaxHeight(max(1, h)).Render(s)
}

// footer is what sits below the chat box: the input, or the front
// approval, with the note input while one is written.
func (m *Model) footer() string {
	switch {
	case m.noting && len(m.pending) > 0:
		input := renderInputBox(m.input.View(), m.width)
		box := m.renderApproval(true)
		if lipgloss.Height(box)+lipgloss.Height(input) > m.height {
			box = m.renderApproval(false) // keep the keys and the note input on screen
		}
		return box + "\n" + input
	case len(m.pending) > 0:
		return m.renderApproval(true)
	}
	if popup := m.renderCommandPopup(); popup != "" {
		return popup + "\n" + renderInputBox(m.input.View(), m.width)
	}
	return renderInputBox(m.input.View(), m.width)
}

// chatBox frames the transcript, or the overlay over it, with any notice
// in its bottom border and, in a sub-agent's conversation, its name in the
// top border.
func (m *Model) chatBox(width int) string {
	if m.overlayText != "" {
		box := styleChatBox.Width(width).Render(m.overlay.View())
		return withBorderLabel(box, cmp.Or(m.notice, "esc to close · pgup/pgdown scroll"))
	}
	box := styleChatBox.Width(width).Render(m.viewport.View())
	if m.viewing != 0 {
		left, right := m.viewLabel()
		box = withTopLabel(box, left, right, styleApprovalBorder)
	}
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
	lines[last] = borderLine("╰", "╯", styleDim.Render(label), "", lipgloss.Width(lines[last]), styleBorderLine)
	return strings.Join(lines, "\n")
}

// withTopLabel writes left- and right-aligned labels into a box's top border.
func withTopLabel(box, left, right string, border lipgloss.Style) string {
	lines := strings.Split(box, "\n")
	lines[0] = borderLine("╭", "╮", left, right, lipgloss.Width(lines[0]), border)
	return strings.Join(lines, "\n")
}

// borderLine draws a width-wide horizontal border with embedded labels.
func borderLine(start, end, left, right string, width int, border lipgloss.Style) string {
	left = ansi.Truncate(left, max(0, width-6), "…")
	used := 5 + ansi.StringWidth(left) // "╭─ " + left + " " + end
	if right != "" {
		used += ansi.StringWidth(right) + 3 // " " + right + " ─"
	}
	if used > width {
		right, used = "", 5+ansi.StringWidth(left)
	}
	line := border.Render(start+"─ ") + left + border.Render(" "+strings.Repeat("─", max(0, width-used)))
	if right != "" {
		line += border.Render(" ") + right + border.Render(" ─")
	}
	return line + border.Render(end)
}
