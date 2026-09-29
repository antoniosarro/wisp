package openaicompat

import (
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func TestThinkFilterFeed(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   []model.Event
	}{
		{
			name:   "plain text, no tags",
			chunks: []string{"hello world"},
			want:   []model.Event{{Kind: model.EventTextDelta, Text: "hello world"}},
		},
		{
			name:   "whole tag in one chunk",
			chunks: []string{"<think>reasoning</think>answer"},
			want: []model.Event{
				{Kind: model.EventReasoningDelta, Reasoning: "reasoning"},
				{Kind: model.EventTextDelta, Text: "answer"},
			},
		},
		{
			name:   "open tag split across chunks",
			chunks: []string{"<thi", "nk>reasoning</think>answer"},
			want: []model.Event{
				{Kind: model.EventReasoningDelta, Reasoning: "reasoning"},
				{Kind: model.EventTextDelta, Text: "answer"},
			},
		},
		{
			name:   "close tag split across chunks",
			chunks: []string{"<think>reasoning</thi", "nk>answer"},
			want: []model.Event{
				{Kind: model.EventReasoningDelta, Reasoning: "reasoning"},
				{Kind: model.EventTextDelta, Text: "answer"},
			},
		},
		{
			name:   "reasoning delivered byte by byte",
			chunks: []string{"<", "t", "h", "i", "n", "k", ">", "a", "b", "<", "/", "t", "h", "i", "n", "k", ">", "c"},
			want: []model.Event{
				{Kind: model.EventReasoningDelta, Reasoning: "a"},
				{Kind: model.EventReasoningDelta, Reasoning: "b"},
				{Kind: model.EventTextDelta, Text: "c"},
			},
		},
		{
			name:   "bare close tag inside a line is a mention",
			chunks: []string{"It closes with `</think>` and", " ends with </think>."},
			want: []model.Event{
				{Kind: model.EventTextDelta, Text: "It closes with `</think>"},
				{Kind: model.EventTextDelta, Text: "` and"},
				{Kind: model.EventTextDelta, Text: " ends with </think>"},
				{Kind: model.EventTextDelta, Text: "."},
			},
		},
		{
			name:   "bare close tag reclassifies earlier text",
			chunks: []string{"plan it", " out</th", "ink>\n\nanswer"},
			want: []model.Event{
				{Kind: model.EventTextDelta, Text: "plan it"},
				{Kind: model.EventTextDelta, Text: " out"},
				{Kind: model.EventReclassify},
				{Kind: model.EventTextDelta, Text: "answer"},
			},
		},
		{
			name:   "opening tag mid-answer stays text",
			chunks: []string{"use <think> tags"},
			want:   []model.Event{{Kind: model.EventTextDelta, Text: "use <think> tags"}},
		},
		{
			name:   "tags after a think block stay text",
			chunks: []string{"<think>r</think>a <think>b</think>"},
			want: []model.Event{
				{Kind: model.EventReasoningDelta, Reasoning: "r"},
				{Kind: model.EventTextDelta, Text: "a <think>b</think>"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &thinkFilter{}
			var got []model.Event
			for _, c := range tt.chunks {
				got = append(got, f.Feed(c)...)
			}
			got = append(got, f.Flush()...)

			if len(got) != len(tt.want) {
				t.Fatalf("got %d events %+v, want %d %+v", len(got), got, len(tt.want), tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestThinkFilterUnclosedTag(t *testing.T) {
	f := &thinkFilter{}
	got := f.Feed("<think>never closes")
	if len(got) != 1 || got[0].Kind != model.EventReasoningDelta || got[0].Reasoning != "never closes" {
		t.Fatalf("Feed = %+v, want a single reasoning event (no partial </think> match to buffer)", got)
	}
	if got := f.Flush(); len(got) != 0 {
		t.Errorf("Flush = %+v, want nothing left buffered", got)
	}
}

func TestThinkFilterUnresolvedPartialTagAtEOF(t *testing.T) {
	f := &thinkFilter{}
	got := f.Feed("<think>reasoning</thi")
	if len(got) != 1 || got[0].Kind != model.EventReasoningDelta || got[0].Reasoning != "reasoning" {
		t.Fatalf("Feed = %+v, want a single reasoning event with the partial close tag buffered", got)
	}
	got = f.Flush()
	if len(got) != 1 || got[0].Kind != model.EventReasoningDelta || got[0].Reasoning != "</thi" {
		t.Errorf("Flush = %+v, want the buffered partial tag emitted as-is", got)
	}
}
