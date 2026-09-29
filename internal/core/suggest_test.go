package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

func TestSuggestNextLeavesHistoryAlone(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{
		{Kind: model.EventReasoningDelta, Reasoning: "hmm"},
		{Kind: model.EventTextDelta, Text: "\"Now add tests for it\"\nextra line"},
		{Kind: model.EventDone},
	}}}
	history := make([]model.Message, 1, 4)
	history[0] = model.Message{Role: model.RoleUser, Content: "write a parser"}

	got, err := SuggestNext(context.Background(), p, nil, history)
	if err != nil || got != "Now add tests for it" {
		t.Fatalf("SuggestNext() = %q, %v", got, err)
	}
	if len(p.LastReq.Messages) != 2 || p.LastReq.Messages[1].Content != suggestPrompt {
		t.Fatalf("request messages = %+v, want history plus the suggestion prompt", p.LastReq.Messages)
	}
	if spare := history[:2]; spare[1].Content != "" {
		t.Fatal("SuggestNext wrote into the caller's history backing array")
	}
}

func TestCleanSuggestionDropsOverlongReplies(t *testing.T) {
	long := make([]rune, maxSuggestionRunes+1)
	for i := range long {
		long[i] = 'a'
	}
	if got := cleanSuggestion(string(long)); got != "" {
		t.Fatalf("cleanSuggestion(long) = %q, want empty", got)
	}
}

// Tools stay in the request, forbidden, so the prompt prefix matches the
// cache; without tools, no tool choice is sent, which some servers reject.
func TestSuggestNextToolChoice(t *testing.T) {
	answer := [][]model.Event{{{Kind: model.EventTextDelta, Text: "ok"}, {Kind: model.EventDone}}}
	history := []model.Message{{Role: model.RoleUser, Content: "hi"}}

	p := &testutil.ScriptedProvider{Turns: answer}
	if _, err := SuggestNext(context.Background(), p, tool.NewRegistry(testutil.EchoTool{}), history); err != nil {
		t.Fatal(err)
	}
	if len(p.LastReq.Tools) != 1 || p.LastReq.ToolChoice != "none" {
		t.Errorf("with tools: %d tools, choice %q; want them sent, forbidden", len(p.LastReq.Tools), p.LastReq.ToolChoice)
	}

	p = &testutil.ScriptedProvider{Turns: answer}
	if _, err := SuggestNext(context.Background(), p, nil, history); err != nil {
		t.Fatal(err)
	}
	if p.LastReq.Tools != nil || p.LastReq.ToolChoice != "" {
		t.Errorf("without tools: tools %v, choice %q; want neither", p.LastReq.Tools, p.LastReq.ToolChoice)
	}
}

func TestCleanSuggestion(t *testing.T) {
	for in, want := range map[string]string{
		"  “Run the tests”  ":         "Run the tests",
		"'Ship it'\nand more":         "Ship it",
		"":                            "",
		"\n\nplain":                   "plain", // leading blank lines are trimmed first
		"Now add tests for the lexer": "Now add tests for the lexer",
	} {
		if got := cleanSuggestion(in); got != want {
			t.Errorf("cleanSuggestion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSuggestNextErrors(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventError, Err: errors.New("boom")}}}}
	if _, err := SuggestNext(context.Background(), p, nil, nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("stream error = %v, want it reported", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p = &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "late"}, {Kind: model.EventDone}}}}
	if _, err := SuggestNext(ctx, p, nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled = %v, want context.Canceled", err)
	}
}
