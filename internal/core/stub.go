package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/textfmt"
)

// maskStub describes masked tool output well enough for the model to know
// what it was, whether it failed, and how to get it back:
//
//	[read internal/core/loop.go lines 1-270: 11.2 KB masked; call read again to see it]
//	[bash "go test ./...": exit 1, 3.4 KB masked; full output in ~/.cache/wisp/masked/…txt
//	first error: loop_test.go:146: expected an error
//	last line: FAIL wisp/internal/core 0.412s]
func maskStub(call model.ToolCall, msg model.Message, spill string, stale bool) string {
	handle := strings.TrimSpace(call.Name + " " + callSubject(call.Args))
	if handle == "" {
		handle = "tool output"
	}
	size := textfmt.Size(int64(len(msg.Content)))
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(handle)
	switch call.Name {
	case "read":
		if first, last := numberedLines(msg.Content); first > 0 {
			fmt.Fprintf(&b, " lines %d-%d", first, last)
		}
		fmt.Fprintf(&b, ": %s masked", size)
	case "grep":
		matches, files := grepCounts(msg.Content)
		fmt.Fprintf(&b, ": %d matches in %d files, %s masked", matches, files, size)
	case "glob", "ls":
		fmt.Fprintf(&b, ": %d entries, %s masked", lineCount(msg.Content), size)
	default:
		if code, ok := exitCode(msg.Content); ok {
			fmt.Fprintf(&b, ": exit %d, %s masked", code, size)
		} else {
			fmt.Fprintf(&b, ": %s masked", size)
		}
	}
	switch {
	case stale:
		b.WriteString("; a later call superseded it")
	case spill == "":
		b.WriteString("; call ")
		b.WriteString(call.Name)
		b.WriteString(" again to see it")
	default:
		b.WriteString(spillMarker)
		b.WriteString(spill)
	}
	// A failure keeps its gist: the model may still need to act on it.
	if code, _ := exitCode(msg.Content); msg.IsError || code != 0 {
		if line := errorLine(msg.Content); line != "" {
			b.WriteString("\nfirst error: ")
			b.WriteString(line)
		}
		b.WriteString("\nlast line: ")
		b.WriteString(lastLine(msg.Content))
	}
	b.WriteString("]")
	return b.String()
}

// callSubject picks the argument that identifies what a call acted on.
func callSubject(args json.RawMessage) string {
	var a map[string]any
	_ = json.Unmarshal(args, &a)
	for _, key := range []string{"command", "path", "pattern", "url", "agent"} {
		if v, ok := a[key].(string); ok && v != "" {
			v = strings.ReplaceAll(v, "\n", " ")
			v = textfmt.Cut(v, 120)
			if key == "command" || key == "pattern" {
				return fmt.Sprintf("%q", v)
			}
			return v
		}
	}
	return ""
}

// Patterns in tool output: read's line numbers, bash's exit code, and
// lines that look like errors.
var (
	numberedLine = regexp.MustCompile(`(?m)^ *(\d+)\t`)
	exitLine     = regexp.MustCompile(`(?m)^\[exit code (-?\d+)\]$`)
	errorish     = regexp.MustCompile(`(?i)\b(error|fail|failed|panic|fatal|exception)\b|^\S+\.\w+:\d+`)
)

// numberedLines returns the first and last line numbers of read output.
func numberedLines(s string) (first, last int) {
	m := numberedLine.FindAllStringSubmatch(s, -1)
	if m == nil {
		return 0, 0
	}
	first, _ = strconv.Atoi(m[0][1])
	last, _ = strconv.Atoi(m[len(m)-1][1])
	return first, last
}

// grepCounts counts grep's "path:line:text" matches and their files.
func grepCounts(s string) (matches, files int) {
	seen := map[string]bool{}
	for line := range strings.Lines(s) {
		path, rest, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, "...") {
			continue
		}
		if n, _, ok := strings.Cut(rest, ":"); ok && strings.Trim(n, "0123456789") == "" {
			matches++
			seen[path] = true
		}
	}
	return matches, len(seen)
}

// exitCode reads the exit code bash appends to failed commands' output.
func exitCode(s string) (int, bool) {
	m := exitLine.FindAllStringSubmatch(s, -1)
	if m == nil {
		return 0, false
	}
	code, _ := strconv.Atoi(m[len(m)-1][1])
	return code, true
}

// errorLine returns the first line that looks like an error, cut to 200
// bytes.
func errorLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); errorish.MatchString(line) && !strings.HasPrefix(line, "[exit code") {
			return textfmt.Cut(line, 200)
		}
	}
	return ""
}

// lastLine returns the last meaningful line of tool output, skipping the
// markers the tools add, cut to 200 bytes.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || strings.HasPrefix(l, "[exit code") || strings.HasPrefix(l, "... (") {
			continue
		}
		return textfmt.Cut(l, 200)
	}
	return ""
}

// lineCount counts lines, a final newline not starting another.
func lineCount(s string) int {
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}
