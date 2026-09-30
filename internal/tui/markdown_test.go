package tui

import (
	"strings"
	"testing"
)

func TestRenderMarkdownAnswerNoCodeIsProseOnly(t *testing.T) {
	got := stripANSI(renderMarkdownAnswer("just plain text, no fences here", 60))
	if !strings.Contains(got, "just plain text, no fences here") {
		t.Errorf("renderMarkdownAnswer() = %q, want the prose preserved", got)
	}
}

func TestRenderMarkdownAnswerHighlightsFencedCode(t *testing.T) {
	text := "here's a snippet:\n\n```go\nfunc main() {}\n```\n\nthat's it"
	got := renderMarkdownAnswer(text, 60)
	plain := stripANSI(got)

	if !strings.Contains(plain, "func main() {}") {
		t.Errorf("rendered = %q, want the code content preserved", plain)
	}
	if !strings.Contains(plain, "go") {
		t.Errorf("rendered = %q, want the language label", plain)
	}
	if !strings.Contains(plain, "that's it") {
		t.Errorf("rendered = %q, want the trailing prose preserved", plain)
	}
	if got == plain {
		t.Error("rendered output has no ANSI styling at all, want the code block highlighted")
	}
}

func TestRenderMarkdownAnswerKeepsListWithNestedCodeIntact(t *testing.T) {
	// A fence indented under a list item is part of that list, not a
	// standalone top-level block. Pulling it out into its own segment
	// would sever the list into two separately-rendered glamour documents
	// (losing continuation/numbering) — this is the bug being regressed
	// against.
	text := "1. do this:\n\n   ```bash\n   echo hi\n   ```\n\n2. then this"

	if n := len(fenceRe.FindAllStringSubmatchIndex(text, -1)); n != 0 {
		t.Fatalf("fenceRe matched %d indented fence(s), want 0 (should be left to glamour, not extracted)", n)
	}

	got := stripANSI(renderMarkdownAnswer(text, 60))
	if !strings.Contains(got, "do this") || !strings.Contains(got, "then this") {
		t.Errorf("rendered = %q, want both list items preserved", got)
	}
	if !strings.Contains(got, "echo hi") {
		t.Errorf("rendered = %q, want the nested code content preserved", got)
	}
}

func TestRenderMarkdownAnswerUnterminatedFenceStaysPlain(t *testing.T) {
	text := "still typing a code block:\n\n```go\nfunc main() {"
	got := stripANSI(renderMarkdownAnswer(text, 60))
	if !strings.Contains(got, "func main() {") {
		t.Errorf("rendered = %q, want the unterminated fence's content still visible", got)
	}
}

func TestRenderMessageAddsMarkerToFirstLineOnly(t *testing.T) {
	got := stripANSI(renderMessage("first\n\nsecond", 80, "●"))
	lines := strings.SplitN(got, "\n", 2)
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "● ") {
		t.Errorf("first line = %q, want it to start with the marker", lines[0])
	}
	if strings.Contains(lines[1], "●") {
		t.Errorf("rest = %q, want no marker", lines[1])
	}
}

func TestRenderProseNeverOverflows(t *testing.T) {
	text := "A small key-value store in Go could prioritize in-memory speed with a sync.Map for concurrency, or trade speed for persistence via file-based serialization (e.g., JSON/protobuf). Alternatives include embedded databases (SQLite), log-structured storage, or disk-backed caches, each balancing performance, scalability, and complexity based on use cases."
	for inner := 20; inner <= 160; inner++ {
		if w := widestLine(renderProse(text, inner)); w > inner {
			t.Fatalf("renderProse at %d produced a %d-wide line", inner, w)
		}
	}
}
