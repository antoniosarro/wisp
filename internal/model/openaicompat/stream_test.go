package openaicompat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

// Stream's own behavior over a real connection: delivery, cancellation,
// the idle timeout, and the one retry of an early drop. What the stream
// says is parsed by readStream, tested in sse_test.go.

func TestStreamDeliversEvents(t *testing.T) {
	srv := sseServer(t, `data: {"choices":[{"delta":{"reasoning_content":"thinking..."}}]}
data: {"choices":[{"delta":{"content":"<think>more</think>answer"}}]}
data: [DONE]
`)
	events, err := New(Config{BaseURL: srv.URL, Model: "m"}, nil).Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []model.Event{
		{Kind: model.EventReasoningDelta, Reasoning: "thinking..."},
		{Kind: model.EventReasoningDelta, Reasoning: "more"},
		{Kind: model.EventTextDelta, Text: "answer"},
		{Kind: model.EventDone},
	}
	got := collect(t, events)
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Text != want[i].Text || got[i].Reasoning != want[i].Reasoning {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestStreamStartFailure(t *testing.T) {
	noRetryDelay(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // closed before use: connection refused on any request

	events, err := New(Config{BaseURL: srv.URL, Model: "m"}, nil).Stream(context.Background(), model.Request{})
	if err == nil || events != nil {
		t.Fatalf("Stream = %v, %v; want a nil channel and an error", events, err)
	}
}

func TestStreamClosesOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // hold the connection open until the client cancels
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	events, err := New(Config{BaseURL: srv.URL, Model: "m"}, nil).Stream(ctx, model.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if first, ok := <-events; !ok || first.Text != "partial" {
		t.Fatalf("first event = %+v, ok=%v, want the partial text delta", first, ok)
	}
	cancel()
	collect(t, events) // fails unless the channel closes
}

func TestStreamIdleTimeout(t *testing.T) {
	defer func(d time.Duration) { streamIdleTimeout = d }(streamIdleTimeout)
	streamIdleTimeout = 200 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // stall, as a hung upstream does
	}))
	defer srv.Close()

	events, err := New(Config{BaseURL: srv.URL, Model: "m"}, nil).Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, events)
	if last := got[len(got)-1]; last.Kind != model.EventError || !errors.Is(last.Err, errStreamIdle) {
		t.Fatalf("a stalled stream ended as %+v, want the idle error", last)
	}
}

// A connection that drops before anything arrives is retried once: nothing
// has reached the caller, so asking again loses nothing.
func TestStreamRetriesAnEarlyDrop(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if hits.Add(1) == 1 {
			w.(http.Flusher).Flush()
			return // dropped: no events, no [DONE]
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	events, err := New(Config{BaseURL: srv.URL, Model: "m"}, nil).Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, events)
	if hits.Load() != 2 || got[len(got)-1].Kind != model.EventDone {
		t.Fatalf("hits = %d, events = %+v", hits.Load(), got)
	}
}

// A drop after the answer started is not retried: the caller already has
// part of it, and a second answer would not continue the first.
func TestStreamDoesNotRetryALateDrop(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n")
	}))
	defer srv.Close()

	events, err := New(Config{BaseURL: srv.URL, Model: "m"}, nil).Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, events)
	if hits.Load() != 1 || got[len(got)-1].Kind != model.EventError {
		t.Fatalf("hits = %d, events = %+v; want one request ending in an error", hits.Load(), got)
	}
}
