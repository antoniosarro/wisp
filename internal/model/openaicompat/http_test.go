package openaicompat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

// countingServer answers every request with handle, counting them.
func countingServer(t *testing.T, handle func(n int32, w http.ResponseWriter)) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handle(calls.Add(1), w)
	}))
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL, Model: "m"}, nil), &calls
}

func TestPostErrorsCarryStatusAndBody(t *testing.T) {
	c, _ := countingServer(t, func(_ int32, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"All non-assistant messages must contain 'content'"}}`))
	})
	_, err := c.post(context.Background(), model.Request{})
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "must contain 'content'") {
		t.Errorf("err = %v, want the status and the server's body, so the real cause is visible", err)
	}
}

func TestPostRetriesTransientFailures(t *testing.T) {
	noRetryDelay(t)
	c, calls := countingServer(t, func(n int32, w http.ResponseWriter) {
		switch n {
		case 1: // dropped connection, like a proxy writing to a closed upstream
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		case 2:
			http.Error(w, "bad gateway", http.StatusBadGateway)
		default:
			w.Header().Set("Content-Type", "text/event-stream")
		}
	})
	resp, err := c.post(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (two transient failures, then success)", got)
	}
}

func TestPostGivesUpAfterRetries(t *testing.T) {
	noRetryDelay(t)
	c, calls := countingServer(t, func(_ int32, w http.ResponseWriter) {
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	})
	if _, err := c.post(context.Background(), model.Request{}); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v, want the final 503", err)
	}
	if got, want := int(calls.Load()), len(retryDelays)+1; got != want {
		t.Errorf("calls = %d, want %d", got, want)
	}
}

func TestPostDoesNotRetryClientErrors(t *testing.T) {
	noRetryDelay(t)
	c, calls := countingServer(t, func(_ int32, w http.ResponseWriter) {
		http.Error(w, "bad request", http.StatusBadRequest)
	})
	if _, err := c.post(context.Background(), model.Request{}); err == nil {
		t.Fatal("expected error on 400")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1: a 400 fails the same way every time", got)
	}
}

func TestPostRetryStopsOnCancel(t *testing.T) {
	c, _ := countingServer(t, func(_ int32, w http.ResponseWriter) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.post(ctx, model.Request{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Error("cancellation did not interrupt the retry wait")
	}
}

func TestRetryAfterIsRead(t *testing.T) {
	c, _ := countingServer(t, func(_ int32, w http.ResponseWriter) {
		w.Header().Set("Retry-After", "3")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	req, _ := c.newRequest(context.Background(), http.MethodGet, c.cfg.BaseURL, nil)
	var se *statusError
	if _, err := c.do(req); !errors.As(err, &se) || se.retryAfter != 3*time.Second {
		t.Errorf("err = %v, want a statusError asking for 3s", err)
	}
}

func TestRetryDelay(t *testing.T) {
	for _, c := range []struct {
		name    string
		attempt int
		err     error
		want    time.Duration
	}{
		{"transport error follows the schedule", 1, errors.New("connection refused"), retryDelays[1]},
		{"longer Retry-After wins", 0, &statusError{code: 429, retryAfter: 3 * time.Second}, 3 * time.Second},
		{"shorter Retry-After is ignored", 2, &statusError{code: 429, retryAfter: time.Second}, retryDelays[2]},
		{"Retry-After is capped", 0, &statusError{code: 503, retryAfter: time.Hour}, maxRetryAfter},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := retryDelay(c.attempt, c.err); got != c.want {
				t.Errorf("retryDelay = %s, want %s", got, c.want)
			}
		})
	}
}

func TestContextOverflowIsRecognized(t *testing.T) {
	for _, c := range []struct {
		code int
		msg  string
		want bool
	}{
		{400, "This model's maximum context length is 8192 tokens", true},
		{400, "request (9000 tokens) exceeds the available context size (8192 tokens)", true},
		{413, "Too many tokens in the request", true},
		{400, "All non-assistant messages must contain 'content'", false},
		{500, "maximum context length exceeded", false}, // a server fault, not our request
	} {
		err := &statusError{code: c.code, msg: c.msg}
		if got := errors.Is(err, model.ErrContextOverflow); got != c.want {
			t.Errorf("%d %q: overflow = %v, want %v", c.code, c.msg, got, c.want)
		}
	}
}

func TestAppAttributionOnlyToOpenRouter(t *testing.T) {
	c := New(Config{APIKey: "k", AppName: "Wisp@1.2.3", AppURL: "https://github.com/antoniosarro/wisp"}, nil)
	req, err := c.newRequest(context.Background(), http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	for h, want := range map[string]string{"HTTP-Referer": "https://github.com/antoniosarro/wisp", "X-OpenRouter-Title": "Wisp@1.2.3", "X-OpenRouter-Categories": "cli-agent"} {
		if got := req.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	req, _ = c.newRequest(context.Background(), http.MethodGet, "http://localhost:8000/v1/models", nil)
	if req.Header.Get("HTTP-Referer") != "" || req.Header.Get("X-OpenRouter-Title") != "" {
		t.Error("attribution headers sent to a non-OpenRouter endpoint")
	}
}
