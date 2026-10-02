package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/core"
)

// renderToolOutput renders an expanded tool result in the same style as
// answers: bullet lists, inline-code paths, and highlighted code cards.
func renderToolOutput(name string, args json.RawMessage, result string, failed bool, width int) string {
	text := strings.Trim(result, "\n")
	if text == "" {
		return styleDim.Render("no output")
	}
	var a struct {
		Path      string `json:"path"`
		Command   string `json:"command"`
		FilesOnly bool   `json:"files_only"`
	}
	_ = json.Unmarshal(args, &a)

	switch {
	case failed && name != "bash":
		return lipgloss.NewStyle().Width(width).Render(styleToolError.Render(text))
	case name == "glob", name == "grep" && a.FilesOnly:
		return renderPathList(text)
	case name == "ls":
		return renderDirListing(text)
	case name == "todo":
		return renderTodos(args)
	case name == agent.ToolName:
		return renderMarkdownAnswer(text, width)
	case name == "fetch":
		var f struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(args, &f)
		return renderCard(styleToolDetail.Render(f.URL), text, width, styleToolCard)
	case name == "web_search":
		var s struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(args, &s)
		return renderCard(styleToolDetail.Render(s.Query), text, width, styleToolCard)
	case name == "grep":
		return renderGrepMatches(text)
	case name == "read":
		return renderCard(styleToolDetail.Render(a.Path), renderNumberedCode(text, filepath.Base(a.Path)), width, styleToolCard)
	case name == "bash":
		card := styleToolCard
		if failed {
			card = styleToolCardFailed
		}
		header := styleToolDetail.Render("$ " + ansi.Truncate(strings.ReplaceAll(a.Command, "\n", " ↵ "), max(1, width-6), "…"))
		return renderCard(header, renderBashOutput(text), width, card)
	default:
		return lipgloss.NewStyle().Width(width).Render(text)
	}
}

// renderCard draws a code-block card with a dim header line.
func renderCard(header, body string, width int, style lipgloss.Style) string {
	return boxed(style, width, header+"\n"+body)
}

// renderNumberedCode highlights read's "<n>\t<line>" output, keeping the
// line numbers as a dim gutter.
func renderNumberedCode(text, filename string) string {
	var nums, code []string
	note := ""
	for _, line := range strings.Split(text, "\n") {
		num, src, ok := strings.Cut(line, "\t")
		if !ok || isToolNote(line) {
			note = line
			continue
		}
		nums = append(nums, strings.TrimSpace(num))
		code = append(code, src)
	}
	width := 0
	for _, n := range nums {
		width = max(width, len(n))
	}
	highlighted := strings.Split(highlightCode(strings.Join(code, "\n"), filename), "\n")
	var b strings.Builder
	for i, n := range nums {
		line := ""
		if i < len(highlighted) {
			line = highlighted[i]
		}
		fmt.Fprintf(&b, "%s  %s\n", styleDim.Render(fmt.Sprintf("%*s", width, n)), line)
	}
	if note != "" {
		b.WriteString(styleDim.Render(strings.TrimSpace(note)))
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderPathList shows glob matches as a bullet list of inline-code paths.
func renderPathList(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if isToolNote(line) {
			b.WriteString(styleDim.Render(strings.TrimSpace(line)))
			b.WriteString("\n")
			continue
		}
		b.WriteString(styleBullet.Render("•"))
		b.WriteString(" ")
		b.WriteString(styleInlineCode.Render(line))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderDirListing shows ls output: directories highlighted, file sizes dim.
func renderDirListing(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		name, size, isFile := strings.Cut(line, "\t")
		switch {
		case isToolNote(line) || strings.HasPrefix(line, "("):
			lines[i] = styleDim.Render(strings.TrimSpace(line))
		case strings.HasSuffix(line, "/"):
			lines[i] = styleDirName.Render(line)
		case isFile:
			lines[i] = name + "  " + styleDim.Render(size)
		}
	}
	return strings.Join(lines, "\n")
}

// renderTodos shows the todo call's task list as a checklist; the tool's
// result only acknowledges it.
func renderTodos(args json.RawMessage) string {
	var a struct {
		Todos []core.Todo `json:"todos"`
	}
	_ = json.Unmarshal(args, &a)
	lines := make([]string, len(a.Todos))
	for i, t := range a.Todos {
		switch t.Status {
		case "completed":
			lines[i] = styleToolOK.Render("✓") + " " + styleDim.Render(t.Content)
		case "in_progress":
			lines[i] = styleSpinner.Render("◐") + " " + styleToolText.Render(t.Content)
		default:
			lines[i] = styleDim.Render("○") + " " + t.Content
		}
	}
	return strings.Join(lines, "\n")
}

// renderGrepMatches groups "path:line:text" matches under their file.
func renderGrepMatches(text string) string {
	var b strings.Builder
	current := ""
	for _, line := range strings.Split(text, "\n") {
		path, rest, ok := strings.Cut(line, ":")
		num, match, ok2 := strings.Cut(rest, ":")
		if isToolNote(line) || !ok || !ok2 {
			b.WriteString(styleDim.Render(strings.TrimSpace(line)))
			b.WriteString("\n")
			continue
		}
		if path != current {
			if current != "" {
				b.WriteString("\n")
			}
			b.WriteString(styleInlineCode.Render(path))
			b.WriteString("\n")
			current = path
		}
		fmt.Fprintf(&b, "  %s %s\n", styleDim.Render(fmt.Sprintf("%4s │", num)), match)
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderBashOutput dims the stderr section and highlights a failing exit code.
func renderBashOutput(text string) string {
	lines := strings.Split(text, "\n")
	inStderr := false
	for i, l := range lines {
		switch {
		case l == "[stderr]":
			inStderr = true
			lines[i] = styleDim.Render("stderr")
		case strings.HasPrefix(l, "[exit code "):
			lines[i] = styleError.Render(strings.Trim(l, "[]"))
		case inStderr:
			lines[i] = styleToolError.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}

// isToolNote reports lines the tools append themselves, like truncation notes.
func isToolNote(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "... (") || strings.HasPrefix(line, "(") && strings.HasSuffix(line, ")")
}
