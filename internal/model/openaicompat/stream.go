package openaicompat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

// Stream issues the request and parses the SSE response into Events. An
// error return means the request never started; after that, failures
// arrive as an EventError. The channel closes after a final EventDone or
// EventError, or once ctx is done.
func (c *Client) Stream(ctx context.Context, req model.Request) (<-chan model.Event, error) {
	// The request has its own context, so a stalled stream can be cut
	// while the error still reaches the caller.
	reqCtx, cancel := context.WithCancelCause(ctx)
	resp, err := c.post(reqCtx, req)
	if err != nil {
		cancel(nil)
		return nil, err
	}
	idle := time.AfterFunc(streamIdleTimeout, func() { cancel(errStreamIdle) })

	events := make(chan model.Event)
	go func() {
		defer close(events)
		defer cancel(nil)
		defer idle.Stop()
		started := false // an event reached the caller
		send := func(e model.Event) bool {
			select {
			case events <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		// A parser bug ends this response, not the process.
		defer func() {
			if p := recover(); p != nil {
				send(errorEvent(fmt.Errorf("parsing the stream: %v", p)))
			}
		}()
		for retried := false; ; retried = true {
			var lost error
			readStream(idleReader{resp.Body, reqCtx, idle}, func(e model.Event) bool {
				if e.Kind == model.EventError && !started && !retried && dropped(e.Err) {
					lost = e.Err
					return false
				}
				started = true
				return send(e)
			})
			_ = resp.Body.Close()
			if lost == nil {
				return
			}
			// The connection dropped before anything arrived: nothing is
			// lost by asking once more, as for a request that failed.
			if resp, err = c.post(reqCtx, req); err != nil {
				send(errorEvent(err))
				return
			}
			idle.Reset(streamIdleTimeout)
		}
	}()
	return events, nil
}

// dropped says whether a stream failed because the connection did, rather
// than the server: only then may asking again help.
func dropped(err error) bool {
	var netErr *net.OpError
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &netErr)
}

// streamIdleTimeout is how long a stream may go without data before it is
// cut. Generous: a local server may take minutes to process a long prompt
// before the first token. A variable so tests can shorten it.
var streamIdleTimeout = 10 * time.Minute

// errStreamIdle is the cause a stream cut for idling ends with.
var errStreamIdle error = idleError{}

type idleError struct{}

func (idleError) Error() string {
	return fmt.Sprintf("no data from the endpoint for %s", streamIdleTimeout)
}

// idleReader restarts the idle timer on every read, and names the cause
// when the timer cut the stream, instead of a bare "context canceled".
type idleReader struct {
	body  io.Reader
	ctx   context.Context
	timer *time.Timer
}

func (r idleReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		r.timer.Reset(streamIdleTimeout)
	}
	if err != nil && errors.Is(context.Cause(r.ctx), errStreamIdle) {
		err = errStreamIdle
	}
	return n, err
}
