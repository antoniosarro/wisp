package span

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSpansNestThroughContext(t *testing.T) {
	sink := record(func(rec *Recorder) {
		ctx, turn := rec.Start(context.Background(), KindTurn, "hello")
		toolCtx, tool := Start(ctx, KindTool, "read")
		_, mcp := Start(toolCtx, KindMCP, "budget/list")
		mcp.End(StatusOK)
		tool.End(StatusOK)
		turn.End(StatusOK)
	})
	got := sink.latest()
	for kind, r := range got {
		if r.Session != "s1" || r.End.IsZero() {
			t.Errorf("%s: session %q, end %v", kind, r.Session, r.End)
		}
	}
	if got[KindTurn].Parent != "" {
		t.Errorf("turn parent = %q, want top level", got[KindTurn].Parent)
	}
	if got[KindTool].Parent != got[KindTurn].ID {
		t.Errorf("tool parent = %q, want turn %q", got[KindTool].Parent, got[KindTurn].ID)
	}
	if got[KindMCP].Parent != got[KindTool].ID {
		t.Errorf("mcp parent = %q, want tool %q", got[KindMCP].Parent, got[KindTool].ID)
	}
}

func TestEndErr(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name       string
		ctx        context.Context
		err        error
		wantStatus string
		wantAttr   any // the "error" attribute
	}{
		{"nil error", context.Background(), nil, StatusOK, nil},
		{"failure", context.Background(), errors.New("boom"), StatusError, "boom"},
		{"cancelled ctx", cancelled, context.Canceled, StatusCancelled, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			sink := record(func(rec *Recorder) {
				_, s := rec.Start(context.Background(), KindTool, "x")
				s.EndErr(c.ctx, c.err)
			})
			got := sink.latest()[KindTool]
			if got.Status != c.wantStatus || got.Attrs["error"] != c.wantAttr {
				t.Errorf("status %q, error attr %v; want %q, %v", got.Status, got.Attrs["error"], c.wantStatus, c.wantAttr)
			}
		})
	}
}

func TestSetCutsLongValues(t *testing.T) {
	long := strings.Repeat("x", maxAttr+10)
	short := json.RawMessage(`{"a":1}`)
	sink := record(func(rec *Recorder) {
		_, s := rec.Start(context.Background(), KindTool, "x")
		s.Set("str", long)
		s.Set("json", json.RawMessage(long))
		s.Set("short", short)
		s.End(StatusOK)
	})
	attrs := sink.latest()[KindTool].Attrs
	want := long[:maxAttr] + cutNote
	for _, key := range []string{"str", "json"} {
		if got, _ := attrs[key].(string); got != want {
			t.Errorf("%s: kept %d bytes, want %d ending in the cut note", key, len(got), len(want))
		}
	}
	if got, ok := attrs["short"].(json.RawMessage); !ok || string(got) != string(short) {
		t.Errorf("short raw JSON = %#v, want it unchanged", attrs["short"])
	}
}

func TestNoRecorderRecordsNothing(t *testing.T) {
	ctx, s := Start(context.Background(), KindTool, "x")
	var rec *Recorder
	_, s2 := rec.Start(ctx, KindTurn, "y")
	if s != nil || s2 != nil {
		t.Fatal("without a recorder, spans should be nil")
	}
	// Every method must be a no-op on nil, so callers never check.
	s.Set("k", 1)
	s.End(StatusOK)
	s2.EndErr(ctx, errors.New("boom"))
	rec.Add(KindMCPConnect, "x", time.Now(), time.Now(), StatusOK, nil)
	rec.Close()
}
