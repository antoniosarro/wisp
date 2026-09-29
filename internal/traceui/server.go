// Package traceui serves `wisp --trace`: a local web page showing each
// session's spans as a timeline, next to its messages. The page is a
// single embedded file (index.html) reading a small JSON API.
//
// Everything is served under a secret path, /<token>/, so only whoever
// wisp printed the URL to can read the transcripts: the server listens on
// localhost, but any user of the machine can reach localhost. guard.go
// refuses the other clients a local server gets: pages from other sites.
package traceui

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/session"
)

//go:embed index.html
var page []byte

// Handler serves the page and its read-only JSON API over store, under
// /token/. The page lists every session store.Dir allows, grouped by
// directory, with here's sessions first. Anything outside /token/ is not
// found.
func Handler(store *session.Store, here, token string) http.Handler {
	root := "/" + token + "/"
	mux := http.NewServeMux()
	// The page's API URLs are relative, so it must be served with the slash.
	mux.Handle("GET /"+token, http.RedirectHandler(root, http.StatusMovedPermanently))
	mux.HandleFunc("GET "+root+"{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET "+root+"api/sessions", func(w http.ResponseWriter, _ *http.Request) {
		sessions, err := store.ListSessions()
		if err != nil {
			fail(w, err)
			return
		}
		ids := make([]string, len(sessions))
		for i, s := range sessions {
			ids[i] = s.ID
		}
		totals, err := store.SpanTotals(ids...)
		if err != nil {
			fail(w, err)
			return
		}
		out := make([]sessionJSON, len(sessions))
		for i, s := range sessions {
			out[i] = sessionJSON{ID: s.ID, Model: s.Model, Created: s.CreatedAt, Preview: s.OneLinePreview(), Dir: s.Dir, Here: s.Dir == here}
			if t, ok := totals[s.ID]; ok {
				out[i].Totals = &totalsJSON{
					Spans: t.Spans, Turns: t.Turns, Requests: t.Requests, Tools: t.Tools, Errors: t.Errors, Open: t.Open,
					Start: ms(t.Start), End: ms(t.End), InputTokens: t.InputTokens, OutputTokens: t.OutputTokens,
					CachedTokens: t.CachedTokens, CostUSD: t.CostUSD,
				}
			}
		}
		send(w, out)
	})
	mux.HandleFunc("GET "+root+"api/sessions/{id}/spans", func(w http.ResponseWriter, r *http.Request) {
		spans, err := store.Spans(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		out := make([]spanJSON, len(spans))
		for i, s := range spans {
			out[i] = spanJSON{ID: s.ID, Parent: s.Parent, Kind: s.Kind, Name: s.Name, Start: ms(s.Start), Status: s.Status, Attrs: s.Attrs}
			if !s.End.IsZero() {
				out[i].End = ms(s.End)
			}
		}
		send(w, out)
	})
	mux.HandleFunc("GET "+root+"api/sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		msgs, err := store.LoadHistory(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		out := make([]messageJSON, len(msgs))
		for i, m := range msgs {
			out[i] = messageJSON{Role: string(m.Role), Content: m.Content, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID, IsError: m.IsError}
		}
		send(w, out)
	})
	return localOnly(mux)
}

// The API's JSON. Times are milliseconds since the epoch, as JavaScript's.
type (
	sessionJSON struct {
		ID      string      `json:"id"`
		Model   string      `json:"model"`
		Created time.Time   `json:"created"`
		Preview string      `json:"preview"` // the first prompt's start
		Dir     string      `json:"dir"`
		Here    bool        `json:"here"`             // started in the directory wisp serves from
		Totals  *totalsJSON `json:"totals,omitempty"` // nil for sessions recorded before spans
	}

	totalsJSON struct {
		Spans        int     `json:"spans"`
		Turns        int     `json:"turns"`
		Requests     int     `json:"requests"`
		Tools        int     `json:"tools"`
		Errors       int     `json:"errors"`
		Open         int     `json:"open"`
		Start        float64 `json:"start"`
		End          float64 `json:"end"`
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		CachedTokens int     `json:"cached_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	}

	spanJSON struct {
		ID     string         `json:"id"`
		Parent string         `json:"parent"`
		Kind   string         `json:"kind"`
		Name   string         `json:"name"`
		Start  float64        `json:"start"`
		End    float64        `json:"end,omitempty"` // absent while open
		Status string         `json:"status"`
		Attrs  map[string]any `json:"attrs"`
	}

	messageJSON struct {
		Role       string           `json:"role"`
		Content    string           `json:"content"`
		ToolCalls  []model.ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
		IsError    bool             `json:"is_error,omitempty"`
	}
)

// ms is t in milliseconds since the epoch, with sub-millisecond precision.
func ms(t time.Time) float64 { return float64(t.UnixNano()) / 1e6 }

// send answers with v as JSON, never cached: the page polls for changes.
func send(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// fail answers with err as a server error.
func fail(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
