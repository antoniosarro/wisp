package session

import (
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/span"
)

var _ span.Sink = (*Store)(nil)

func TestSpansRoundTrip(t *testing.T) {
	s := openTestStore(t)
	start := time.Unix(1000, 500)
	open := span.Record{ID: "a", Session: "s1", Kind: span.KindTurn, Name: "hi", Start: start, Attrs: map[string]any{"n": 1}}
	child := span.Record{ID: "b", Parent: "a", Session: "s1", Kind: span.KindTool, Name: "read", Start: start.Add(time.Second), End: start.Add(2 * time.Second), Status: span.StatusOK, Attrs: map[string]any{}}
	other := span.Record{ID: "c", Session: "s2", Kind: span.KindTurn, Start: start, Attrs: map[string]any{}}
	for _, r := range []span.Record{open, child, other} {
		if err := s.WriteSpan(r); err != nil {
			t.Fatal(err)
		}
	}
	closed := open
	closed.End, closed.Status = start.Add(3*time.Second), span.StatusOK
	if err := s.WriteSpan(closed); err != nil {
		t.Fatal(err)
	}

	got, err := s.Spans("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("spans = %+v, want a then b", got)
	}
	if !got[0].End.Equal(closed.End) || got[0].Status != span.StatusOK || got[0].Attrs["n"] != float64(1) {
		t.Errorf("a = %+v, want the closed write", got[0])
	}
	if got[1].Parent != "a" || !got[1].Start.Equal(child.Start) {
		t.Errorf("b = %+v", got[1])
	}
}

func TestSpanTotals(t *testing.T) {
	s := openTestStore(t)
	start := time.Unix(1000, 0)
	for _, r := range []span.Record{
		{ID: "t", Session: "s1", Kind: span.KindTurn, Start: start, End: start.Add(5 * time.Second), Status: span.StatusOK},
		{ID: "r1", Parent: "t", Session: "s1", Kind: span.KindRequest, Start: start, End: start.Add(time.Second), Status: span.StatusOK,
			Attrs: map[string]any{"gen_ai.usage.input_tokens": 100, "gen_ai.usage.output_tokens": 10, "wisp.usage.cached_tokens": 80, "wisp.cost.usd": 0.5}},
		{ID: "r2", Parent: "t", Session: "s1", Kind: span.KindRequest, Start: start.Add(2 * time.Second), End: start.Add(3 * time.Second), Status: span.StatusOK,
			Attrs: map[string]any{"gen_ai.usage.input_tokens": 200, "gen_ai.usage.output_tokens": 20, "wisp.usage.cost_usd": 0.25}},
		{ID: "x", Parent: "t", Session: "s1", Kind: span.KindTool, Start: start.Add(time.Second), End: start.Add(2 * time.Second), Status: span.StatusDenied},
		{ID: "o", Parent: "t", Session: "s1", Kind: span.KindTool, Start: start.Add(6 * time.Second)},           // still open
		{ID: "sub", Parent: "x", Session: "s1", Kind: span.KindTurn, Start: start, End: start.Add(time.Second)}, // a sub-agent's turn
		{ID: "other", Session: "s2", Kind: span.KindTurn, Start: start},
	} {
		if r.Attrs == nil {
			r.Attrs = map[string]any{}
		}
		if err := s.WriteSpan(r); err != nil {
			t.Fatal(err)
		}
	}
	totals, err := s.SpanTotals("s1", "s3")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := totals["s1"]
	if !ok || len(totals) != 1 {
		t.Fatalf("totals = %+v, want s1 only: s2 wasn't asked for, s3 has no spans", totals)
	}
	want := SpanTotals{Spans: 6, Turns: 1, Requests: 2, Tools: 2, Errors: 1, Open: 1,
		Start: start, End: start.Add(6 * time.Second), InputTokens: 300, OutputTokens: 30, CachedTokens: 80, CostUSD: 0.75}
	if got != want {
		t.Errorf("totals = %+v\nwant     %+v", got, want)
	}
	if none, err := s.SpanTotals(); err != nil || len(none) != 0 {
		t.Errorf("no ids = %v, %v", none, err)
	}
}
