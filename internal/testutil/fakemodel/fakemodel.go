// Package fakemodel is a scriptable OpenAI-compatible model server for
// end-to-end tests. A script lists rules; each chat request is answered by
// the first unused rule whose condition matches the request's last
// message, so the fake reacts to what wisp sends (a failing check, a tool
// result) instead of replaying a fixed sequence. Every chat request is
// recorded, and Check (check.go) verifies invariants every request must
// satisfy.
package fakemodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Script is what the server serves.
type Script struct {
	// Models are the /v1/models entries, with any fields a real server
	// adds (max_model_len, pricing, ...). Default: one model named "fake".
	Models []map[string]any `yaml:"models"`
	// Routes answers other paths ("GET /props", "POST /api/show") with
	// the given JSON. Unlisted paths get 404, as on servers without them.
	Routes map[string]any `yaml:"routes"`
	Rules  []Rule         `yaml:"rules"`
}

// Rule answers a request whose last message matches When.
type Rule struct {
	When   When  `yaml:"when"`
	Repeat bool  `yaml:"repeat"` // may answer more than once
	Reply  Reply `yaml:"reply"`
}

// When matches a request's last message; empty fields match anything.
type When struct {
	Role     string `yaml:"role"`
	Contains string `yaml:"contains"`
	Model    string `yaml:"model"` // the request's model, not the message's
}

// Reply is a streamed response, or an error status.
type Reply struct {
	Reasoning  string     `yaml:"reasoning"` // sent as reasoning_content deltas
	Text       string     `yaml:"text"`
	ToolCalls  []ToolCall `yaml:"tool_calls"`
	Chunk      int        `yaml:"chunk"`      // bytes of text per SSE event; 0 sends it whole
	Delay      Duration   `yaml:"delay"`      // pause between SSE events
	Finish     string     `yaml:"finish"`     // finish_reason; default stop or tool_calls
	Status     int        `yaml:"status"`     // non-200 answers with Error instead of a stream
	Error      string     `yaml:"error"`      // body of an error status
	Disconnect bool       `yaml:"disconnect"` // end the stream before [DONE]
	NoUsage    bool       `yaml:"no_usage"`   // omit the usage chunk
}

// ToolCall is one call in a Reply. ID defaults to call_N, numbered across
// the server's replies.
type ToolCall struct {
	ID   string         `yaml:"id"`
	Name string         `yaml:"name"`
	Args map[string]any `yaml:"args"`
}

// Duration reads YAML durations like "20ms".
type Duration time.Duration

// UnmarshalYAML parses the node with time.ParseDuration.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	*d = Duration(v)
	return err
}

// Load reads a YAML script.
func Load(path string) (Script, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Script{}, err
	}
	var s Script
	if err := yaml.Unmarshal(data, &s); err != nil {
		return Script{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Server is a running fake.
type Server struct {
	URL string // OpenAI-compatible base URL, ending in /v1

	srv     *httptest.Server
	logPath string

	mu       sync.Mutex // guards the fields below; requests may be concurrent
	script   Script
	used     []bool // rules that answered, by index
	requests []json.RawMessage
	calls    int // tool calls sent, for default IDs
}

// Start serves script until Close. If logPath is set, each chat request is
// also appended to it as a JSON line, for scripts to grep.
func Start(script Script, logPath string) *Server {
	if len(script.Models) == 0 {
		script.Models = []map[string]any{{"id": "fake"}}
	}
	s := &Server{script: script, used: make([]bool, len(script.Rules)), logPath: logPath}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.srv.URL + "/v1"
	return s
}

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// Requests returns the chat requests received so far, compacted.
func (s *Server) Requests() []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]json.RawMessage(nil), s.requests...)
}

// serve routes a request: the model listing, chat, or a scripted route.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	switch key := r.Method + " " + r.URL.Path; {
	case key == "GET /v1/models":
		writeJSON(w, map[string]any{"object": "list", "data": s.script.Models})
	case key == "POST /v1/chat/completions":
		s.chat(w, r)
	case s.script.Routes[key] != nil:
		writeJSON(w, s.script.Routes[key])
	default:
		http.NotFound(w, r)
	}
}

// request is the part of a chat request the rules look at.
type request struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
}

// chat records a chat request and answers it with the matching rule's
// reply. A request no rule matches is a 500 naming the message, so a
// scenario that goes somewhere unexpected fails with a clear reason.
func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil || len(req.Messages) == 0 {
		http.Error(w, "fakemodel: bad chat request", http.StatusBadRequest)
		return
	}
	s.record(body)

	last := req.Messages[len(req.Messages)-1]
	reply, ok := s.match(req.Model, last.Role, text(last.Content))
	if !ok {
		http.Error(w, fmt.Sprintf("fakemodel: no rule matches a %s message: %.200q", last.Role, text(last.Content)), http.StatusInternalServerError)
		return
	}
	if reply.Status != 0 && reply.Status != http.StatusOK {
		http.Error(w, reply.Error, reply.Status)
		return
	}
	s.stream(w, reply, len(body))
}

// record keeps body, compacted, and appends it to the log file if any.
func (s *Server) record(body []byte) {
	var compact bytes.Buffer
	_ = json.Compact(&compact, body) // body already parsed as JSON
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, json.RawMessage(compact.String()))
	if s.logPath == "" {
		return
	}
	if f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.WriteString(compact.String() + "\n")
		_ = f.Close()
	}
}

// match returns the reply of the first unused rule matching the message,
// and marks it used unless it repeats.
func (s *Server) match(model, role, content string) (Reply, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, rule := range s.script.Rules {
		w := rule.When
		if s.used[i] && !rule.Repeat ||
			w.Role != "" && w.Role != role ||
			w.Model != "" && w.Model != model ||
			!strings.Contains(content, w.Contains) {
			continue
		}
		s.used[i] = true
		return rule.Reply, true
	}
	return Reply{}, false
}

// text flattens message content: a string, or content parts.
func text(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, part := range c {
			if p, ok := part.(map[string]any); ok {
				t, _ := p["text"].(string)
				b.WriteString(t)
			}
		}
		return b.String()
	}
	return ""
}
