package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/agent"
)

// renderUserPrompt draws the prompt as a full-width panel with an accent bar.
func renderUserPrompt(input string, width int) string {
	inner := max(1, width-chatContentLeft)
	lines := strings.Split(ansi.Wrap(input, inner, ""), "\n")
	for i, l := range lines {
		lines[i] = styleUserBar.Render("▌") + styleUserPrompt.Width(inner+1).Render(" "+l)
	}
	return styleChatMargin.Render(strings.Join(lines, "\n"))
}

// elapsedSince is how long ago t was, for the live timers on screen;
// snapshot tests replace it so their output doesn't depend on timing.
var elapsedSince = time.Since

// renderAnswer renders a finished answer as Markdown.
func renderAnswer(text string, width int) string {
	return renderMessage(text, width, styleAnswerPrefix.Render("●"))
}

// renderLiveAnswer renders an answer that is still streaming.
func renderLiveAnswer(text string, width int) string {
	return renderMessageLive(text, width, styleAnswerPrefix.Render("●"), true)
}

// renderNotice renders a note from wisp itself.
func renderNotice(text string, width int) string {
	return renderMessage(text, width, styleNoticePrefix.Render("◇"))
}

// renderMessage renders markdown with marker on its first line; the marker
// is added after rendering so it isn't parsed as markdown.
func renderMessage(text string, width int, marker string) string {
	return renderMessageLive(text, width, marker, false)
}

// renderMessageLive renders like renderMessage; live marks text still
// streaming (see renderMarkdown).
func renderMessageLive(text string, width int, marker string, live bool) string {
	body := renderMarkdown(text, max(10, width-chatContentLeft), live)
	indent := strings.Repeat(" ", chatPrefixWidth)
	return styleChatMargin.Render(marker + " " + strings.ReplaceAll(body, "\n", "\n"+indent))
}

// renderTurnError shows why a turn stopped.
func renderTurnError(msg string, width int) string {
	wrapped := lipgloss.NewStyle().Width(max(10, width-chatMarginLeft)).Render(styleError.Render("✗ " + msg))
	return styleChatMargin.Render(wrapped)
}

// renderReasoningSummary is collapsed reasoning: its length and duration.
func renderReasoningSummary(text string, took time.Duration) string {
	summary := fmt.Sprintf("▸ Thought for %d words", len(strings.Fields(text)))
	if took > 0 {
		summary += " · " + formatDuration(took)
	}
	return styleReasoning.Render(summary)
}

// formatDuration renders d as "4.2s" or "1m 05s".
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	d = d.Round(time.Second)
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// renderReasoningFull shows reasoning behind a dim left rule.
func renderReasoningFull(text, suffix string, width int) string {
	body := lipgloss.NewStyle().Width(max(10, width-chatMarginLeft-2)).Render(styleReasoning.Render(text) + suffix)
	rule := styleReasoningBorder.Render("│") + " "
	return styleReasoningHeader.Render("▾ Thinking") + "\n" + rule + strings.ReplaceAll(body, "\n", "\n"+rule)
}

// toolDetail summarizes a call's args by its most telling argument.
func toolDetail(name string, args json.RawMessage) string {
	switch name {
	case "todo":
		return todoProgress(args)
	case agent.ToolName:
		var a struct {
			Agent string `json:"agent"`
			Task  string `json:"task"`
		}
		_ = json.Unmarshal(args, &a)
		return a.Agent + ": " + ansi.Truncate(strings.Join(strings.Fields(a.Task), " "), 80, "…")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(args, &fields) == nil {
		for _, key := range []string{"command", "pattern", "url", "path"} {
			var value string
			if json.Unmarshal(fields[key], &value) == nil && value != "" {
				return ansi.Truncate(strings.ReplaceAll(value, "\n", " ↵ "), 100, "…")
			}
		}
	}
	if s := strings.Trim(strings.TrimSpace(string(args)), "{}"); s != "" {
		return ansi.Truncate(strings.Join(strings.Fields(s), " "), 100, "…")
	}
	return ""
}

// todoProgress reports how many tasks a todo call marks completed.
func todoProgress(args json.RawMessage) string {
	var a struct {
		Todos []struct {
			Status string `json:"status"`
		} `json:"todos"`
	}
	_ = json.Unmarshal(args, &a)
	done := 0
	for _, t := range a.Todos {
		if t.Status == "completed" {
			done++
		}
	}
	return fmt.Sprintf("%d/%d done", done, len(a.Todos))
}

// toolHeader is a call's icon, name, and most telling argument.
func toolHeader(icon, name string, args json.RawMessage) string {
	header := icon + " " + styleToolText.Render(name)
	if detail := toolDetail(name, args); detail != "" {
		header += " " + styleToolDetail.Render(detail)
	}
	return header
}

// renderToolRunning draws an in-flight call; activity describes a
// sub-agent's current step.
func renderToolRunning(spinnerView, name string, args json.RawMessage, activity string, elapsed time.Duration, width int) string {
	meta := " · " + formatDuration(elapsed)
	if activity != "" {
		meta += " · " + activity
	}
	meta += " · esc to interrupt"
	return boxed(styleToolCardRunning, width, toolHeader(spinnerView, name, args)+styleDim.Render(meta))
}

// renderToolResult draws a finished call as a one-line summary; a
// collapsed failure also shows the start of its error.
func renderToolResult(name string, args json.RawMessage, result string, took time.Duration, width int, failed, expanded bool) string {
	icon, card := styleToolOK.Render("✓"), styleToolCard
	if failed {
		icon, card = styleToolFailed.Render("✗"), styleToolCardFailed
	}
	text := strings.TrimSpace(result)
	line := toolHeader(icon, name, args)
	if name == "todo" {
		if took > 0 {
			line += styleDim.Render(" · " + formatToolTime(took))
		}
	} else {
		line += styleDim.Render(" · " + toolMeta(text, took))
	}
	if failed && !expanded && text != "" {
		first, _, _ := strings.Cut(text, "\n")
		line += "  " + styleToolError.Render(ansi.Truncate(first, 60, "…"))
	}
	return boxed(card, width, line)
}

// toolMeta summarizes a result's size and the call's duration.
func toolMeta(text string, took time.Duration) string {
	meta := "no output"
	// A final newline ends the last line; it doesn't start another.
	if n := strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1; text != "" && n == 1 {
		meta = "1 line"
	} else if text != "" {
		meta = fmt.Sprintf("%d lines", n)
	}
	if took > 0 {
		meta += " · " + formatToolTime(took)
	}
	return meta
}

// formatToolTime shows sub-second tool runs in milliseconds.
func formatToolTime(took time.Duration) string {
	if took < time.Second {
		return fmt.Sprintf("%dms", max(1, took.Milliseconds()))
	}
	return formatDuration(took)
}

// renderToolDenied draws a call the user denied.
func renderToolDenied(name string, args json.RawMessage, width int) string {
	return boxed(styleToolCardDenied, width, toolHeader(styleToolDenied.Render("⊘"), name, args)+" "+styleToolDenied.Render("denied"))
}

// displayPath abbreviates the home directory to ~ and, if width > 0, keeps
// the tail of p within width cells.
func displayPath(p string, width int) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(p, home); ok && (rest == "" || rest[0] == os.PathSeparator) {
			p = "~" + rest
		}
	}
	if w := ansi.StringWidth(p); width > 0 && w > width {
		p = ansi.TruncateLeft(p, w-width+1, "…")
	}
	return p
}

// renderInputBox frames the prompt input.
func renderInputBox(inputView string, width int) string {
	return boxed(styleInputBox, width, inputView)
}
