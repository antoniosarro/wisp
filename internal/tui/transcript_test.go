package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// plain is t rendered at width w without its styling.
func plain(t transcript, w int) string {
	return ansi.Strip(t.render(w, "*"))
}

func TestTranscriptStreamsIntoOneEntry(t *testing.T) {
	var tr transcript
	tr.apply(model.Event{Kind: model.EventReasoningDelta, Reasoning: "think"})
	tr.apply(model.Event{Kind: model.EventReasoningDelta, Reasoning: "ing"})
	tr.apply(model.Event{Kind: model.EventTextDelta, Text: "hel"})
	tr.apply(model.Event{Kind: model.EventTextDelta, Text: "lo"})

	if len(tr) != 2 || tr[0].kind != entryReasoning || tr[0].text != "thinking" || tr[1].kind != entryAnswer || tr[1].text != "hello" {
		t.Errorf("transcript = %+v, want reasoning then answer", tr)
	}
}

func TestTranscriptReclassifiesAnswerAsReasoning(t *testing.T) {
	var tr transcript
	tr.apply(model.Event{Kind: model.EventTextDelta, Text: "was thinking"})
	tr.apply(model.Event{Kind: model.EventReclassify})
	tr.apply(model.Event{Kind: model.EventTextDelta, Text: "answer"})

	if len(tr) != 2 || tr[0].kind != entryReasoning || tr[0].text != "was thinking" || tr[1].text != "answer" {
		t.Errorf("transcript = %+v, want the first text moved to reasoning", tr)
	}
}

func TestTranscriptResolvesCallsInOrder(t *testing.T) {
	var tr transcript
	// No IDs, as some providers send parallel calls.
	for _, name := range []string{"read", "grep"} {
		tr.apply(model.Event{Kind: model.EventToolCall, ToolCall: &model.ToolCall{Name: name, Args: json.RawMessage(`{}`)}})
	}
	tr.resolve(ToolResultMsg{Call: model.ToolCall{Name: "read"}, Result: tool.Result{Content: "ok"}})
	tr.resolve(ToolResultMsg{Call: model.ToolCall{Name: "grep"}, Err: errors.New("bad pattern\nmore")})

	if tr[0].status != toolOK || tr[1].status != toolFailed || tr[1].detail != "bad pattern" {
		t.Errorf("transcript = %+v, want read ok and grep failed with its first line", tr)
	}
}

func TestTranscriptSettle(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{fmt.Errorf("stream: %w", context.Canceled), "Interrupted."},
		{errors.New("endpoint down"), "✗ endpoint down"},
	} {
		var tr transcript
		tr.apply(model.Event{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "1", Name: "bash"}})
		tr.settle(tc.err)
		if tr[0].status != toolFailed {
			t.Errorf("settle(%v) left the call %v, want failed", tc.err, tr[0].status)
		}
		if got := plain(tr, 80); !strings.Contains(got, tc.want) {
			t.Errorf("settle(%v) shows %q, want %q", tc.err, got, tc.want)
		}
	}
}

// An escape sequence split across deltas is only recognizable once they
// are joined: sanitizing each delta on its own would print its tail.
func TestTranscriptSanitizesJoinedDeltas(t *testing.T) {
	var tr transcript
	tr.apply(model.Event{Kind: model.EventTextDelta, Text: "\x1b]0;pwn"})
	tr.apply(model.Event{Kind: model.EventTextDelta, Text: "ed\x07ok"})
	tr.apply(model.Event{Kind: model.EventToolCall, ToolCall: &model.ToolCall{Name: "bash\x1b[2J", Args: json.RawMessage("{\"c\":\"\x1b[31m\"}")}})
	tr.resolve(ToolResultMsg{Call: model.ToolCall{}, Result: tool.Result{IsError: true, Content: "\x1b]52;c;aGk=\x07failed"}})

	got := tr.render(80, "*")
	if strings.Contains(got, "\x1b]") || strings.Contains(got, "\x1b[2J") || strings.Contains(got, "\x1b[31m") {
		t.Errorf("render kept an escape sequence from the model or a tool: %q", got)
	}
	if p := ansi.Strip(got); !strings.HasPrefix(p, "● ok\n") || !strings.Contains(p, "failed") {
		t.Errorf("render = %q, want the answer \"ok\" and the call's error", p)
	}
}

func TestTranscriptWrapsToWidth(t *testing.T) {
	var tr transcript
	tr.add(entryUser, strings.Repeat("word ", 20))
	tr.add(entryAnswer, strings.Repeat("word ", 20))
	tr.apply(model.Event{Kind: model.EventToolCall, ToolCall: &model.ToolCall{Name: "write", Args: json.RawMessage(`{"content":"` + strings.Repeat("x", 200) + `"}`)}})

	for _, line := range strings.Split(tr.render(30, "*"), "\n") {
		if w := ansi.StringWidth(line); w > 30 {
			t.Errorf("line %q is %d columns, want at most 30", ansi.Strip(line), w)
		}
	}
}
