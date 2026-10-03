package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/mcp"
)

// Grid of the /context view: each cell is 1/contextCells of the window.
const (
	contextCols  = 20
	contextRows  = 10
	contextCells = contextCols * contextRows
)

// contextUsage is what a /context block shows: the budget snapshot and
// the model it was taken for.
type contextUsage struct {
	stats core.ContextStats
	model string
}

// showContext adds a /context block. Between turns the numbers are read
// from the loop; during one, the last step's snapshot is used, since the
// loop is busy.
func (m *Model) showContext() {
	stats := m.stats.Context
	if !m.inTurn {
		stats = m.loop.ContextUsage()
	}
	m.appendBlock(block{kind: blockContext, usage: &contextUsage{stats, m.opts.Model.ID}})
}

// contextCategory is one share of the window.
type contextCategory struct {
	label  string
	tokens int
	glyph  string
	style  lipgloss.Style
}

// categories splits what requests send, in the order the grid fills.
// Empty categories are left out.
func (u contextUsage) categories() []contextCategory {
	c := u.stats
	mcpTools, tools := 0, 0
	for name, n := range c.Tools {
		if name == mcp.SearchToolName || name == mcp.CallToolName {
			mcpTools += n
		} else {
			tools += n
		}
	}
	fill := func(color lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(color) }
	all := []contextCategory{
		{"System prompt", c.Prompt, "■", fill(colorLavender)},
		{"Project instructions (AGENTS.md)", c.Project, "■", fill(colorAmber)},
		{"Tool definitions", tools, "■", fill(colorViolet)},
		{"MCP servers", c.MCPPrompt + mcpTools, "■", fill(colorMint)},
		{"Summary of earlier messages", c.Summary, "■", fill(colorMagenta)},
		{"Messages", c.Messages, "■", fill(colorCyan)},
	}
	var out []contextCategory
	for _, cat := range all {
		if cat.tokens > 0 {
			out = append(out, cat)
		}
	}
	return out
}

// renderContextBlock draws the grid beside the legend, or above it when
// the chat is too narrow for both.
func renderContextBlock(u contextUsage, width int) string {
	grid := u.grid()
	legend := u.legend()
	var body string
	switch {
	case grid == "":
		body = legend
	case width >= lipgloss.Width(grid)+4+lipgloss.Width(legend):
		body = lipgloss.JoinHorizontal(lipgloss.Top, grid, "    ", legend)
	default:
		body = grid + "\n\n" + legend
	}
	return styleChatMargin.Render(styleReasoningHeader.Render("Context usage") + "\n" + body)
}

// grid fills contextCells cells: each category's share in order, then
// free space, with the reply's reserve at the end. Empty without a window.
func (u contextUsage) grid() string {
	c := u.stats
	if c.Window <= 0 {
		return ""
	}
	cellsFor := func(tokens int) int {
		if tokens <= 0 {
			return 0
		}
		return max(1, int(math.Round(float64(tokens)*contextCells/float64(c.Window))))
	}
	var cells []string
	for _, cat := range u.categories() {
		for range cellsFor(cat.tokens) {
			cells = append(cells, cat.style.Render(cat.glyph))
		}
	}
	reserve := cellsFor(c.Reserve)
	cells = cells[:min(len(cells), contextCells-reserve)]
	for len(cells) < contextCells-reserve {
		cells = append(cells, styleDim.Render("·"))
	}
	for range reserve {
		cells = append(cells, styleDim.Render("×"))
	}
	var rows []string
	for r := range contextRows {
		rows = append(rows, strings.Join(cells[r*contextCols:(r+1)*contextCols], " "))
	}
	return strings.Join(rows, "\n")
}

// legend names the model, the total, each category with its share, and
// what compaction is doing.
func (u contextUsage) legend() string {
	c := u.stats
	used := c.Used()
	var b strings.Builder
	if u.model != "" {
		b.WriteString(styleToolText.Render(sanitize(u.model))) // from the endpoint
		b.WriteString("\n")
	}
	pct := func(n int) string {
		if c.Window <= 0 {
			return ""
		}
		return fmt.Sprintf(" (%.1f%%)", float64(n)*100/float64(c.Window))
	}
	if c.Window > 0 {
		fmt.Fprintf(&b, "%s / %s tokens (%.1f%%)\n\n", shortTokens(used), shortTokens(c.Window), c.UsedPct())
	} else {
		fmt.Fprintf(&b, "%s tokens · window unknown\n\n", shortTokens(used))
	}
	b.WriteString(styleDim.Render("Estimated usage by category"))
	b.WriteString("\n")
	for _, cat := range u.categories() {
		fmt.Fprintf(&b, "%s %s: %s tokens%s\n", cat.style.Render(cat.glyph), cat.label, shortTokens(cat.tokens), styleDim.Render(pct(cat.tokens)))
	}
	if c.Window > 0 {
		free := max(0, c.Window-used-c.Reserve)
		fmt.Fprintf(&b, "%s Free space: %s tokens%s\n", styleDim.Render("·"), shortTokens(free), styleDim.Render(pct(free)))
		fmt.Fprintf(&b, "%s Reserved for the reply: %s tokens%s\n", styleDim.Render("×"), shortTokens(c.Reserve), styleDim.Render(pct(c.Reserve)))
		fmt.Fprintf(&b, "\n%s\n", styleDim.Render(fmt.Sprintf("History masks past %s, summarizes if still over", shortTokens(c.MaskTrigger()))))
	}
	var notes []string
	if n := c.MaskedResults + c.MaskedCalls; n > 0 {
		notes = append(notes, fmt.Sprintf("%d masked, saving ~%s", n, shortTokens(c.MaskedSaved)))
	}
	if c.Compactions > 0 {
		notes = append(notes, fmt.Sprintf("%d compaction%s", c.Compactions, map[bool]string{true: "s"}[c.Compactions > 1]))
	}
	if c.Ratio != 0 && c.Ratio != 1 {
		notes = append(notes, fmt.Sprintf("estimates ×%.2f to the server's counts", c.Ratio))
	}
	if len(notes) > 0 {
		b.WriteString(styleDim.Render(strings.Join(notes, " · ")))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
