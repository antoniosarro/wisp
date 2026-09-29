package openaicompat

import (
	"context"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/span"
)

// parse runs readStream over an SSE body and returns every event.
func parse(body string) []model.Event {
	var got []model.Event
	readStream(strings.NewReader(body), func(e model.Event) bool { got = append(got, e); return true })
	return got
}

// toolCalls returns the tool calls among events.
func toolCalls(events []model.Event) []model.ToolCall {
	var out []model.ToolCall
	for _, e := range events {
		if e.Kind == model.EventToolCall {
			out = append(out, *e.ToolCall)
		}
	}
	return out
}

// sseServer answers every request with body as an event stream.
func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// collect reads events until the channel closes, failing after 2s.
func collect(t *testing.T, events <-chan model.Event) []model.Event {
	t.Helper()
	var got []model.Event
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, e)
		case <-timeout:
			t.Fatal("timed out waiting for events")
		}
	}
}

// noRetryDelay makes retries immediate for the duration of the test.
func noRetryDelay(t *testing.T) {
	t.Helper()
	saved := retryDelays
	retryDelays = make([]time.Duration, len(saved))
	t.Cleanup(func() { retryDelays = saved })
}

// captured is what chatServer last received.
type captured struct {
	mu     sync.Mutex
	body   string
	header http.Header
}

func (c *captured) get() (string, http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body, c.header
}

// chatServer answers every request with an empty stream, keeping the last
// request's body and headers.
func chatServer(t *testing.T) (*httptest.Server, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.mu.Lock()
		got.body, got.header = string(b), r.Header.Clone()
		got.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// redirectClient dials srv for every host, so a Client can use a real
// base URL such as OpenRouter's, whose behavior depends on the host.
func redirectClient(srv *httptest.Server) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}}
}

// postBody posts req with c and returns the body the server received.
func postBody(t *testing.T, c *Client, got *captured, req model.Request) string {
	t.Helper()
	return postBodyCtx(t, context.Background(), c, got, req)
}

func postBodyCtx(t *testing.T, ctx context.Context, c *Client, got *captured, req model.Request) string {
	t.Helper()
	resp, err := c.post(ctx, req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	body, _ := got.get()
	return body
}

// fakeServer answers the given routes ("GET /v1/models") and 404s the
// rest, as each server does for the others' native endpoints.
func fakeServer(t *testing.T, routes map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL + "/v1"}, nil)
}

// round6 rounds to 6 decimals: float products like 0.00000027*1e6 aren't
// exact.
func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// spanSink keeps the latest write of a span.
type spanSink struct {
	mu   sync.Mutex
	last span.Record
}

func (s *spanSink) WriteSpan(r span.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = r
	return nil
}
