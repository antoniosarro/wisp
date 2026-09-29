package session

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/span"
)

// The store is the span.Sink of a session's recorder: spans are written
// when they start and again when they end, and the trace page reads them
// back.

// WriteSpan stores a span, replacing an earlier write of the same span.
func (s *Store) WriteSpan(r span.Record) error {
	attrs, err := json.Marshal(r.Attrs)
	if err != nil {
		return err
	}
	var end int64
	if !r.End.IsZero() {
		end = r.End.UnixNano()
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO spans (id, parent_id, session_id, kind, name, start_ns, end_ns, status, attrs) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Parent, r.Session, r.Kind, r.Name, r.Start.UnixNano(), end, r.Status, string(attrs))
	return err
}

// Spans returns a session's spans in start order.
func (s *Store) Spans(sessionID string) ([]span.Record, error) {
	rows, err := s.db.Query(`SELECT id, parent_id, session_id, kind, name, start_ns, end_ns, status, attrs FROM spans WHERE session_id = ? ORDER BY start_ns, rowid`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []span.Record
	for rows.Next() {
		var r span.Record
		var start, end int64
		var attrs string
		if err := rows.Scan(&r.ID, &r.Parent, &r.Session, &r.Kind, &r.Name, &start, &end, &r.Status, &attrs); err != nil {
			return nil, err
		}
		r.Start = time.Unix(0, start)
		if end != 0 {
			r.End = time.Unix(0, end)
		}
		if err := json.Unmarshal([]byte(attrs), &r.Attrs); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// SpanTotals sums up one session's spans.
type SpanTotals struct {
	Spans, Turns, Requests, Tools int // Turns: the user's, not sub-agents'
	Errors, Open                  int // spans that failed or were denied, and still running
	Start, End                    time.Time
	InputTokens, OutputTokens     int
	CachedTokens                  int
	CostUSD                       float64 // billed where reported, else at list price
}

// SpanTotals returns totals per session for ids, of those that have
// spans. The trace page asks for the sessions it lists, every few seconds,
// so the rest of the database isn't read.
func (s *Store) SpanTotals(ids ...string) (map[string]SpanTotals, error) {
	totals := map[string]SpanTotals{}
	if len(ids) == 0 {
		return totals, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT session_id, COUNT(*),
		SUM(kind = 'turn' AND parent_id = ''), SUM(kind = 'request'), SUM(kind = 'tool'),
		SUM(status IN ('error', 'denied')), SUM(end_ns = 0),
		MIN(start_ns), MAX(CASE WHEN end_ns = 0 THEN start_ns ELSE end_ns END),
		COALESCE(SUM(json_extract(attrs, '$."gen_ai.usage.input_tokens"')), 0),
		COALESCE(SUM(json_extract(attrs, '$."gen_ai.usage.output_tokens"')), 0),
		COALESCE(SUM(json_extract(attrs, '$."wisp.usage.cached_tokens"')), 0),
		COALESCE(SUM(COALESCE(json_extract(attrs, '$."wisp.cost.usd"'), json_extract(attrs, '$."wisp.usage.cost_usd"'))), 0)
		FROM spans WHERE session_id IN (?`+strings.Repeat(",?", len(ids)-1)+`) GROUP BY session_id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var t SpanTotals
		var start, end int64
		if err := rows.Scan(&id, &t.Spans, &t.Turns, &t.Requests, &t.Tools, &t.Errors, &t.Open, &start, &end,
			&t.InputTokens, &t.OutputTokens, &t.CachedTokens, &t.CostUSD); err != nil {
			return nil, err
		}
		t.Start, t.End = time.Unix(0, start), time.Unix(0, end)
		totals[id] = t
	}
	return totals, rows.Err()
}
