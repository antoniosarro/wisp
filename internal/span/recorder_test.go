package span

import (
	"context"
	"sync"
	"testing"
	"time"
)

// memSink keeps every write in order; the session store keeps only the
// latest write of each span, which latest reproduces.
type memSink struct {
	mu     sync.Mutex
	writes []Record
}

func (m *memSink) WriteSpan(r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes = append(m.writes, r)
	return nil
}

// latest returns the last write of each span, keyed by kind; tests use one
// span per kind.
func (m *memSink) latest() map[string]Record {
	got := map[string]Record{}
	for _, r := range m.writes {
		got[r.Kind] = r
	}
	return got
}

// record runs fn with a recorder for session "s1" and returns the sink
// after Close has flushed it.
func record(fn func(*Recorder)) *memSink {
	sink := &memSink{}
	rec := NewRecorder(sink, "s1")
	fn(rec)
	rec.Close()
	return sink
}

func TestSpanIsWrittenAtStartAndEnd(t *testing.T) {
	sink := record(func(rec *Recorder) {
		_, s := rec.Start(context.Background(), KindTurn, "hello")
		s.Set("k", "v")
		s.End(StatusOK)
	})
	if len(sink.writes) != 2 {
		t.Fatalf("writes = %d, want 2", len(sink.writes))
	}
	open, closed := sink.writes[0], sink.writes[1]
	if open.ID != closed.ID {
		t.Errorf("start and end wrote different IDs: %q, %q", open.ID, closed.ID)
	}
	// The open write lets the UI show spans still running.
	if !open.End.IsZero() || open.Status != "" || len(open.Attrs) != 0 {
		t.Errorf("open write = %+v, want no end, status, or attrs", open)
	}
	if closed.End.IsZero() || closed.Status != StatusOK || closed.Attrs["k"] != "v" {
		t.Errorf("closed write = %+v", closed)
	}
}

func TestAddWritesFinishedTopLevelSpan(t *testing.T) {
	start := time.Now().Add(-time.Second)
	end := time.Now()
	sink := record(func(rec *Recorder) {
		rec.Add(KindMCPConnect, "budget", start, end, StatusOK, map[string]any{"wisp.mcp.tools": 3})
	})
	if len(sink.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(sink.writes))
	}
	got := sink.writes[0]
	if got.ID == "" || got.Parent != "" || got.Session != "s1" || !got.Start.Equal(start) || !got.End.Equal(end) ||
		got.Status != StatusOK || got.Attrs["wisp.mcp.tools"] != 3 {
		t.Errorf("Add wrote %+v", got)
	}
}

func TestSpansAfterCloseAreDropped(t *testing.T) {
	sink := &memSink{}
	rec := NewRecorder(sink, "s1")
	ctx, turn := rec.Start(context.Background(), KindTurn, "t")
	rec.Close()

	// None of these may panic on the closed queue or reach the sink.
	turn.End(StatusCancelled)
	_, late := Start(ctx, KindTool, "late")
	late.End(StatusOK)
	rec.Add(KindMCPConnect, "x", time.Now(), time.Now(), StatusOK, nil)
	rec.Close()

	if len(sink.writes) != 1 {
		t.Errorf("writes = %d, want only the turn's start", len(sink.writes))
	}
}
