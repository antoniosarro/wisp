package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/permission"
)

// maxWritePreviewLines is how much of a new file's content a write
// approval shows.
const maxWritePreviewLines = 12

// approvalArgs are the arguments of the tools whose approval shows more
// than their JSON.
type approvalArgs struct {
	Path       string `json:"path"`
	Command    string `json:"command"`
	Content    string `json:"content"`
	Old        string `json:"old_string"`
	New        string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
	URL        string `json:"url"`
	Edits      []struct {
		Old        string `json:"old_string"`
		New        string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	} `json:"edits"`
}

// permissionDetails describes a pending call for the approval box.
func permissionDetails(name string, args json.RawMessage, workDir string) string {
	// The file on disk is found by the path as sent; what the box shows
	// is escaped, shown as ␛, ␍, ...: the user must see exactly what runs.
	var raw struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(args, &raw)
	args = sanitizeJSON(args, showControls)
	var a approvalArgs
	_ = json.Unmarshal(args, &a)

	var head, body string
	switch name {
	case "bash":
		head, body = "wants to run a command", "$ "+styleCommand.Render(strings.ReplaceAll(a.Command, "\n", "\n  "))
	case "edit":
		head, body = "wants to edit "+styleCommand.Render(a.Path)+landsAt(raw.Path), editDiff(a.Old, a.New, a.ReplaceAll)
	case "multi_edit":
		head = fmt.Sprintf("wants to make %d edits to %s", len(a.Edits), styleCommand.Render(a.Path)+landsAt(raw.Path))
		parts := make([]string, len(a.Edits))
		for i, e := range a.Edits {
			parts[i] = styleDim.Render(fmt.Sprintf("edit %d", i+1)) + "\n" + editDiff(e.Old, e.New, e.ReplaceAll)
		}
		body = strings.Join(parts, "\n\n")
	case "fetch":
		head, body = "wants to download a web page", styleCommand.Render(a.URL)
	case "write":
		head, body = writeSummary(raw.Path, a), writePreview(raw.Path, a.Content)
	default:
		head, body = "wants to use a tool", prettyJSON(args)
	}
	details := styleToolText.Render(name) + " " + head + "\n\n" + body
	if workDir != "" {
		details += "\n\n" + styleDim.Render("in "+showControls(displayPath(workDir, 0)))
	}
	return details
}

// editDiff shows an edit as the lines it removes, then the lines it adds.
func editDiff(old, replacement string, replaceAll bool) string {
	var b strings.Builder
	for _, l := range strings.Split(old, "\n") {
		b.WriteString(styleDiffRemoved.Render("- " + l))
		b.WriteString("\n")
	}
	for _, l := range strings.Split(replacement, "\n") {
		b.WriteString(styleDiffAdded.Render("+ " + l))
		b.WriteString("\n")
	}
	if replaceAll {
		b.WriteString(styleDim.Render("every occurrence will be replaced"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeSummary says whether a write creates or overwrites path, where it
// lands, and how long the content is. a is the escaped arguments.
func writeSummary(path string, a approvalArgs) string {
	verb := "wants to create "
	if _, err := os.Stat(path); err == nil {
		verb = "wants to overwrite "
	}
	return verb + styleCommand.Render(a.Path) + landsAt(path) + styleDim.Render(" · "+toolMeta(a.Content, 0))
}

// landsAt names where a write to path lands when that is another file
// (permission.LinkTarget).
func landsAt(path string) string {
	target := permission.LinkTarget(path)
	if target == "" {
		return ""
	}
	return styleApprovalTitle.Render(" → " + showControls(displayPath(target, 0)))
}

// maxPreviewBytes caps the existing file a write preview diffs against.
const maxPreviewBytes = 1 << 20

// writePreview shows the escaped content of a new file's first lines or,
// for an existing text file at path, the lines that change.
func writePreview(path, content string) string {
	// Only a regular file of sane size: reading a FIFO or a device would
	// block the UI, so the call couldn't even be denied.
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() || info.Size() > maxPreviewBytes {
		return contentPreview(content)
	}
	old, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(old, 0) >= 0 {
		return contentPreview(content)
	}
	old = []byte(showControls(string(old)))
	if diff, ok := lineDiff(string(old), content); ok {
		return diff
	}
	return styleDim.Render("too large to diff; the new content starts:") + "\n" + contentPreview(content)
}

// contentPreview is content's first lines and how many more there are.
func contentPreview(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	preview := styleToolPreview.Render(strings.Join(lines[:min(len(lines), maxWritePreviewLines)], "\n"))
	if n := len(lines) - maxWritePreviewLines; n > 0 {
		preview += "\n" + styleDim.Render(fmt.Sprintf("… %d more lines", n))
	}
	return preview
}

// Limits and markers of lineDiff.
const (
	maxDiffCells               = 4_000_000 // LCS table limit, about 16 MB
	diffContext                = 2         // unchanged lines shown around each change
	maxDiffShown               = 400       // rendered diff lines before the rest is summarized
	diffKeep, diffDel, diffAdd = ' ', '-', '+'
)

// lineDiff renders the lines that differ between oldText and newText with
// a little context, reporting false when the change is too large to compare.
func lineDiff(oldText, newText string) (string, bool) {
	a := strings.Split(strings.TrimRight(oldText, "\n"), "\n")
	b := strings.Split(strings.TrimRight(newText, "\n"), "\n")
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(ma) == 0 && len(mb) == 0 {
		return styleDim.Render("no changes"), true
	}
	if (len(ma)+1)*(len(mb)+1) > maxDiffCells {
		return "", false
	}

	// lcs[i][j] is the longest common subsequence of ma[i:] and mb[j:].
	lcs := make([][]int32, len(ma)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(mb)+1)
	}
	for i := len(ma) - 1; i >= 0; i-- {
		for j := len(mb) - 1; j >= 0; j-- {
			if ma[i] == mb[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte
		text string
	}
	var ops []op
	for _, l := range a[:pre] {
		ops = append(ops, op{diffKeep, l})
	}
	i, j := 0, 0
	for i < len(ma) || j < len(mb) {
		switch {
		case i < len(ma) && j < len(mb) && ma[i] == mb[j]:
			ops = append(ops, op{diffKeep, ma[i]})
			i, j = i+1, j+1
		case j < len(mb) && (i == len(ma) || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, op{diffAdd, mb[j]})
			j++
		default:
			ops = append(ops, op{diffDel, ma[i]})
			i++
		}
	}
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, op{diffKeep, l})
	}

	near := make([]bool, len(ops))
	for k, o := range ops {
		if o.kind != diffKeep {
			for c := max(0, k-diffContext); c <= min(len(ops)-1, k+diffContext); c++ {
				near[c] = true
			}
		}
	}
	var out []string
	gap := false
	for k, o := range ops {
		if !near[k] {
			gap = true
			continue
		}
		if gap && len(out) > 0 {
			out = append(out, styleDim.Render("  …"))
		}
		gap = false
		switch o.kind {
		case diffDel:
			out = append(out, styleDiffRemoved.Render("- "+o.text))
		case diffAdd:
			out = append(out, styleDiffAdded.Render("+ "+o.text))
		default:
			out = append(out, styleDim.Render("  "+o.text))
		}
	}
	if n := len(out) - maxDiffShown; n > 0 {
		out = append(out[:maxDiffShown], styleDim.Render(fmt.Sprintf("… %d more diff lines", n)))
	}
	return strings.Join(out, "\n"), true
}

// prettyJSON indents raw for display, or returns it as it is if invalid.
func prettyJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		return string(raw)
	}
	return b.String()
}

// renderApproval frames the pending request with its title, position in
// the queue, and the always-visible key hints.
// details false leaves out the request's details, for screens too short
// to show them next to the note input.
func (m *Model) renderApproval(details bool) string {
	req := m.pending[0]
	answers := styleKeyAllow.Render("y") + " allow   " +
		styleKeyAllow.Render("a") + " always allow " + showControls(permission.RuleLabel(req.Name, req.Args)) + "   " +
		styleKeyDeny.Render("n") + " deny   " +
		styleKeyDeny.Render("t") + " deny with note"
	rest := styleKeyCancel.Render("esc") + " cancel turn" + styleDim.Render("   ↑↓ scroll")
	// Say what's out of view: padding could otherwise push the part that
	// matters below the box.
	if hidden := m.approval.TotalLineCount() - m.approval.VisibleLineCount(); details && hidden > 0 {
		rest = styleApprovalTitle.Render(fmt.Sprintf("%d more line%s ↑↓", hidden, map[bool]string{true: "s"}[hidden > 1])) + "   " + styleKeyCancel.Render("esc") + " cancel turn"
	}
	inner := max(1, m.width-4)
	keys := answers + "   " + rest
	if ansi.StringWidth(keys) > inner {
		keys = ansi.Truncate(answers, inner, "…") + "\n" + ansi.Truncate(rest, inner, "…")
	}
	content := keys
	if details {
		content = m.approval.View() + "\n\n" + keys
	}
	box := stylePermissionBox.Width(max(1, m.width-2)).Render(content)
	count := ""
	if len(m.pending) > 1 {
		count = styleDim.Render(fmt.Sprintf("1 of %d", len(m.pending)))
	}
	return withTopLabel(box, styleApprovalTitle.Render("Permission required"), count, styleApprovalBorder)
}
