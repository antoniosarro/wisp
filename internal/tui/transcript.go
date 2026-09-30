package tui

import (
	"context"
	"errors"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/model"
)

// entryKind is what an entry in the transcript shows.
type entryKind int

const (
	entryUser entryKind = iota
	entryAnswer
	entryReasoning
	entryTool
	entryNotice
	entryError
)

// toolStatus is where a tool call entry's call stands.
type toolStatus int

const (
	toolRunning toolStatus = iota
	toolOK
	toolFailed
)

// entry is one item of the transcript. Its text is kept as it arrived and
// sanitized when rendered: an escape sequence split across stream deltas
// is only whole, and so only removable, once they are joined.
type entry struct {
	kind   entryKind
	text   string
	callID string     // entryTool: the call's ID, which its result echoes
	status toolStatus // entryTool
	detail string     // entryTool: the first line of a failed call's error
}

// transcript is the conversation as the UI shows it, oldest first.
type transcript []entry

// add appends an entry of kind showing text.
func (t *transcript) add(kind entryKind, text string) {
	*t = append(*t, entry{kind: kind, text: text})
}

// appendText extends the last entry if it is of kind, so a streamed
// answer or reasoning grows in place, and starts a new one otherwise.
func (t *transcript) appendText(kind entryKind, text string) {
	if n := len(*t); n > 0 && (*t)[n-1].kind == kind {
		(*t)[n-1].text += text
		return
	}
	t.add(kind, text)
}

// apply adds a streamed event. Errors are not shown here: the turn ends
// with them, and settle reports them.
func (t *transcript) apply(e model.Event) {
	switch e.Kind {
	case model.EventTextDelta:
		t.appendText(entryAnswer, e.Text)
	case model.EventReasoningDelta:
		t.appendText(entryReasoning, e.Reasoning)
	case model.EventReclassify:
		// Only this response's text is reclassified, and a response's
		// text is the last entry: tool calls end a response.
		if n := len(*t); n > 0 && (*t)[n-1].kind == entryAnswer {
			text := (*t)[n-1].text
			*t = (*t)[:n-1]
			t.appendText(entryReasoning, text)
		}
	case model.EventToolCall:
		if c := e.ToolCall; c != nil {
			*t = append(*t, entry{kind: entryTool, text: c.Name + " " + string(c.Args), callID: c.ID})
		}
	}
}

// resolve marks the first running call with msg's ID finished. Matching
// the first, not the last, keeps calls resolved in order when a provider
// sends parallel calls without IDs.
func (t *transcript) resolve(msg ToolResultMsg) {
	for i := range *t {
		e := &(*t)[i]
		if e.kind != entryTool || e.status != toolRunning || e.callID != msg.Call.ID {
			continue
		}
		switch {
		case msg.Err != nil:
			e.status, e.detail = toolFailed, firstLine(msg.Err.Error())
		case msg.Result.IsError:
			e.status, e.detail = toolFailed, firstLine(msg.Result.Content)
		default:
			e.status = toolOK
		}
		return
	}
}

// settle ends a turn: calls it left running failed, and its error, if
// any, is shown.
func (t *transcript) settle(err error) {
	for i := range *t {
		if e := &(*t)[i]; e.kind == entryTool && e.status == toolRunning {
			e.status, e.detail = toolFailed, "not finished"
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		t.add(entryNotice, "Interrupted.")
	case err != nil:
		t.add(entryError, err.Error())
	}
}

// firstLine is s up to its first newline.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// render lays the transcript out at width w, one blank line between
// entries; spin is the spinner frame shown on running calls.
func (t transcript) render(w int, spin string) string {
	parts := make([]string, 0, len(t))
	for _, e := range t {
		parts = append(parts, e.render(w, spin))
	}
	return strings.Join(parts, "\n\n")
}

// render lays e out at width w.
func (e entry) render(w int, spin string) string {
	text := strings.ReplaceAll(sanitize(e.text), "\t", "    ")
	switch e.kind {
	case entryUser:
		return prefixed(styleUserBar.Render("▌ "), styleUserBar.Render("▌ "), styleUserPrompt, text, w)
	case entryAnswer:
		return prefixed(styleAnswerPrefix.Render("● "), "  ", lipgloss.NewStyle(), strings.TrimSpace(text), w)
	case entryReasoning:
		return prefixed("  ", "  ", styleReasoning, strings.TrimSpace(text), w)
	case entryNotice:
		return prefixed(styleNoticePrefix.Render("· "), "  ", styleDim, text, w)
	case entryError:
		return prefixed(styleError.Render("✗ "), "  ", styleError, text, w)
	}
	// A call is one line: its arguments can be a whole file.
	var mark string
	switch e.status {
	case toolRunning:
		mark = spin
	case toolOK:
		mark = styleToolOK.Render("✓")
	case toolFailed:
		mark = styleToolFailed.Render("✗")
	}
	name, args, _ := strings.Cut(strings.ReplaceAll(text, "\n", " "), " ")
	line := ansi.Truncate(styleToolText.Render(name)+" "+styleToolDetail.Render(args), max(1, w-2), "…")
	if e.detail != "" {
		detail := ansi.Truncate(sanitize(e.detail), max(1, w-4), "…")
		line += "\n  " + styleToolFailed.Render(detail)
	}
	return mark + " " + line
}

// prefixed wraps text to width w behind first on its first line and rest
// on the others, rendering the text in style.
func prefixed(first, rest string, style lipgloss.Style, text string, w int) string {
	lines := strings.Split(ansi.Wrap(text, max(1, w-2), ""), "\n")
	for i, l := range lines {
		p := rest
		if i == 0 {
			p = first
		}
		lines[i] = p + style.Render(l)
	}
	return strings.Join(lines, "\n")
}
