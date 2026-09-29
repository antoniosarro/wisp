package fakemodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"
)

// stream writes reply as SSE: reasoning, text, tool calls, the finish
// reason, usage, and [DONE]. A Disconnect reply stops after the text.
func (s *Server) stream(w http.ResponseWriter, reply Reply, promptBytes int) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	event := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(time.Duration(reply.Delay))
	}
	delta := func(d map[string]any, finish any) {
		event(map[string]any{"choices": []any{map[string]any{"delta": d, "finish_reason": finish}}})
	}

	for _, part := range chunks(reply.Reasoning, reply.Chunk) {
		delta(map[string]any{"reasoning_content": part}, nil)
	}
	for _, part := range chunks(reply.Text, reply.Chunk) {
		delta(map[string]any{"content": part}, nil)
	}
	if reply.Disconnect {
		return
	}
	for i, call := range reply.ToolCalls {
		s.mu.Lock()
		s.calls++
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", s.calls)
		}
		s.mu.Unlock()
		args, _ := json.Marshal(call.Args)
		delta(map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": id, "type": "function",
			"function": map[string]any{"name": call.Name, "arguments": string(args)},
		}}}, nil)
	}
	finish := reply.Finish
	if finish == "" {
		finish = "stop"
		if len(reply.ToolCalls) > 0 {
			finish = "tool_calls"
		}
	}
	delta(map[string]any{}, finish)
	if !reply.NoUsage {
		// Deterministic stand-ins for token counts: a quarter of the bytes.
		completion := (len(reply.Reasoning) + len(reply.Text)) / 4
		for _, c := range reply.ToolCalls {
			completion += len(c.Name) + 10
		}
		event(map[string]any{"choices": []any{}, "usage": map[string]any{
			"prompt_tokens": promptBytes / 4, "completion_tokens": completion, "total_tokens": promptBytes/4 + completion,
		}})
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

// chunks splits s into pieces of about n bytes, between characters as a
// real server's deltas are; n <= 0 keeps it whole.
func chunks(s string, n int) []string {
	if s == "" {
		return nil
	}
	if n <= 0 {
		return []string{s}
	}
	var out []string
	for len(s) > n {
		end := n
		for end < len(s) && !utf8.RuneStart(s[end]) {
			end++
		}
		out = append(out, s[:end])
		s = s[end:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// writeJSON answers with v as JSON.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
