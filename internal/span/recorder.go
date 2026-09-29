package span

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Recorder writes spans to a Sink from one goroutine, so the loop does not
// wait on the database. Up to 256 records are buffered; past that, a send
// waits for the writer to catch up rather than dropping spans.
type Recorder struct {
	session string
	sink    Sink
	queue   chan Record
	done    chan struct{} // closed once the writer has drained queue

	mu     sync.RWMutex // guards closed against sends
	closed bool
}

// NewRecorder records spans of session to sink until Close.
func NewRecorder(sink Sink, session string) *Recorder {
	r := &Recorder{session: session, sink: sink, queue: make(chan Record, 256), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		for rec := range r.queue {
			_ = r.sink.WriteSpan(rec) // tracing must never break a session
		}
	}()
	return r
}

// Close writes what's queued and stops the recorder. Spans ending later,
// e.g. a turn cancelled at exit, are dropped. Close is safe to call more
// than once and on a nil *Recorder.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.mu.Unlock()
	<-r.done
}

// send queues rec for the writer, or drops it once the recorder is closed.
// The read lock keeps Close from closing queue mid-send.
func (r *Recorder) send(rec Record) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.closed {
		r.queue <- rec
	}
}

// Start opens a span under the one ctx carries, or at the top level of the
// session when there is none.
func (r *Recorder) Start(ctx context.Context, kind, name string) (context.Context, *Span) {
	if r == nil {
		return Start(ctx, kind, name)
	}
	parentID := ""
	if parent := FromContext(ctx); parent != nil {
		parentID = parent.record.ID
	}
	return r.start(ctx, parentID, kind, name)
}

// start opens a span under parent (empty for the top level), writes it so
// the open span is visible, and returns ctx carrying it.
func (r *Recorder) start(ctx context.Context, parent, kind, name string) (context.Context, *Span) {
	s := &Span{rec: r, record: Record{
		ID: newID(), Parent: parent, Session: r.session, Kind: kind, Name: name,
		Start: time.Now(), Attrs: map[string]any{},
	}}
	s.write()
	return context.WithValue(ctx, spanKey{}, s), s
}

// Add records a finished span at the top level, e.g. one that happened
// before the recorder existed.
func (r *Recorder) Add(kind, name string, start, end time.Time, status string, attrs map[string]any) {
	if r == nil {
		return
	}
	r.send(Record{ID: newID(), Session: r.session, Kind: kind, Name: name, Start: start, End: end, Status: status, Attrs: attrs})
}

// newID returns a random 16-hex-digit span ID, the size OpenTelemetry uses.
func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // never fails on supported platforms
	return hex.EncodeToString(b[:])
}
