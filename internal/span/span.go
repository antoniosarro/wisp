// Package span records what a session does as nested, timed spans, in the
// shape OpenTelemetry uses: a turn holds its model requests and tool calls,
// a tool call holds its approval wait, MCP call, or sub-agent run. Spans
// travel in a context, so a child finds its parent without being told.
package span

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"time"

	"github.com/antoniosarro/wisp/internal/textfmt"
)

// Kinds of span. Attribute names follow OpenTelemetry's GenAI conventions
// where one exists (gen_ai.*).
const (
	KindTurn       = "turn"        // a user message to the final answer
	KindRequest    = "request"     // one model request
	KindTool       = "tool"        // one tool call
	KindApproval   = "approval"    // waiting for the user to allow a call
	KindMCP        = "mcp"         // a call to an MCP server
	KindMCPConnect = "mcp.connect" // starting an MCP server and listing its tools
)

// Statuses a span ends with.
const (
	StatusOK        = "ok"
	StatusError     = "error"
	StatusDenied    = "denied"
	StatusSkipped   = "skipped"
	StatusCancelled = "cancelled"
)

// Record is a span as stored: End is zero while the span is open.
type Record struct {
	ID      string
	Parent  string // ID of the enclosing span; empty at the top level
	Session string
	Kind    string
	Name    string
	Start   time.Time
	End     time.Time
	Status  string // empty while open
	Attrs   map[string]any
}

// Sink stores records; writing one with an ID already stored replaces it.
// A span is written twice, when it opens and when it ends.
type Sink interface {
	WriteSpan(Record) error
}

// Span is an open span. A nil *Span is valid and records nothing, so code
// can instrument unconditionally.
type Span struct {
	rec *Recorder

	mu     sync.Mutex // guards record, which Set and End change
	record Record
}

// spanKey is the context key a *Span is stored under.
type spanKey struct{}

// FromContext returns the span ctx carries, if any.
func FromContext(ctx context.Context) *Span {
	s, _ := ctx.Value(spanKey{}).(*Span)
	return s
}

// Start opens a span under the one ctx carries; without one it records
// nothing and returns a nil *Span. Use [Recorder.Start] to open a
// top-level span.
func Start(ctx context.Context, kind, name string) (context.Context, *Span) {
	parent := FromContext(ctx)
	if parent == nil {
		return ctx, nil
	}
	return parent.rec.start(ctx, parent.record.ID, kind, name)
}

// maxAttr caps a string attribute, in bytes: enough for any real prompt
// part, not for a runaway tool output.
const maxAttr = 256 << 10

// cutNote marks an attribute value cut at maxAttr.
const cutNote = "\n[cut at 256 KB]"

// Set records an attribute. Strings and raw JSON longer than maxAttr are
// cut, and then stored as a string. Attributes set after End are not
// written.
func (s *Span) Set(key string, value any) {
	if s == nil {
		return
	}
	switch v := value.(type) {
	case string:
		if len(v) > maxAttr {
			value = textfmt.Prefix(v, maxAttr) + cutNote
		}
	case json.RawMessage:
		if len(v) > maxAttr {
			value = textfmt.Prefix(string(v), maxAttr) + cutNote
		}
	}
	s.mu.Lock()
	s.record.Attrs[key] = value
	s.mu.Unlock()
}

// End closes the span with status and writes it.
func (s *Span) End(status string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.record.End, s.record.Status = time.Now(), status
	s.mu.Unlock()
	s.write()
}

// EndErr ends the span as ok, cancelled, or failed, from err. An error
// after ctx is done counts as a cancellation, not a failure; a failure
// keeps its message in the "error" attribute.
func (s *Span) EndErr(ctx context.Context, err error) {
	switch {
	case err == nil:
		s.End(StatusOK)
	case ctx.Err() != nil:
		s.End(StatusCancelled)
	default:
		s.Set("error", err.Error())
		s.End(StatusError)
	}
}

// write sends a snapshot of the record, so later Set calls cannot race
// with the sink reading its attributes.
func (s *Span) write() {
	s.mu.Lock()
	rec := s.record
	rec.Attrs = maps.Clone(s.record.Attrs)
	s.mu.Unlock()
	s.rec.send(rec)
}
