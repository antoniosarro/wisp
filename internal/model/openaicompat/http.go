package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

// newRequest builds a request to target with the client's credentials,
// and OpenRouter's app attribution when target is OpenRouter.
func (c *Client) newRequest(ctx context.Context, method, target string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	if c.cfg.AppURL != "" && isOpenRouter(target) {
		req.Header.Set("HTTP-Referer", c.cfg.AppURL) // identifies the app; required for the rest
		req.Header.Set("X-OpenRouter-Title", c.cfg.AppName)
		req.Header.Set("X-OpenRouter-Categories", "cli-agent")
	}
	return req, nil
}

// maxErrorBody caps how much of an error response is kept in the error.
const maxErrorBody = 4096

// do sends req and turns a non-200 status into a *statusError.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		se := &statusError{code: resp.StatusCode, msg: fmt.Sprintf("%s: %s", resp.Status, bytes.TrimSpace(body))}
		// Only the seconds form; an HTTP date falls back to our own delays.
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			se.retryAfter = time.Duration(secs) * time.Second
		}
		return nil, se
	}
	return resp, nil
}

// fetchJSON sends body (if any) as JSON and decodes a 200 response into out.
func (c *Client) fetchJSON(ctx context.Context, method, target string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, method, target, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s: %w", target, err)
	}
	return nil
}

// statusError is a non-200 response, or an error chunk mid-stream,
// carrying the body so the real cause is visible.
type statusError struct {
	code       int // HTTP status, or the error chunk's code; 0 if it had none
	msg        string
	retryAfter time.Duration // what a 429 or 503 asked to wait, if it said
}

func (e *statusError) Error() string { return e.msg }

// overflowPhrases are how vLLM, llama.cpp, OpenAI, and OpenRouter word a
// rejection of a request longer than the context window.
var overflowPhrases = []string{"context length", "context_length", "context size", "context window", "maximum context", "too many tokens"}

// Unwrap exposes model.ErrContextOverflow for rejections of an overlong
// request, so callers can tell them apart from other bad requests.
func (e *statusError) Unwrap() error {
	if e.code != http.StatusBadRequest && e.code != http.StatusRequestEntityTooLarge {
		return nil
	}
	msg := strings.ToLower(e.msg)
	for _, s := range overflowPhrases {
		if strings.Contains(msg, s) {
			return model.ErrContextOverflow
		}
	}
	return nil
}

// retryDelays are the waits before each retry of a chat request that failed
// before streaming began; their count is the number of retries.
var retryDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// maxRetryAfter caps how long a Retry-After header can make a retry wait.
const maxRetryAfter = time.Minute

// retryable reports whether a failed request may succeed if sent again:
// transport errors (dropped or refused connections), rate limits, and
// server errors. Other 4xx responses would fail the same way again, and a
// cancelled request was given up on.
func retryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.code == http.StatusTooManyRequests || se.code >= 500
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// retryDelay is the wait before retry attempt+1 after err: the scheduled
// delay, or a longer Retry-After the server asked for (a rate limit's
// window, e.g. OpenRouter's), capped at maxRetryAfter.
func retryDelay(attempt int, err error) time.Duration {
	delay := retryDelays[attempt]
	if se := (*statusError)(nil); errors.As(err, &se) && se.retryAfter > delay {
		delay = min(se.retryAfter, maxRetryAfter)
	}
	return delay
}
