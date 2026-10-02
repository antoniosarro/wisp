package traceui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/session"
	"github.com/antoniosarro/wisp/internal/span"
)

// token is the secret path the tests serve under.
const token = "t0k3n"

func TestHandler(t *testing.T) {
	store, err := session.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	store.Dir = "/here"
	id, err := store.CreateSession("spark-4b")
	if err != nil {
		t.Fatal(err)
	}
	store.Dir = "/elsewhere"
	old, _ := store.CreateSession("spark-4b") // from before tracing: no spans
	if err := store.AppendMessage(old, model.Message{Role: model.RoleUser, Content: "older"}); err != nil {
		t.Fatal(err) // stored, so listed, from its first message
	}
	store.Dir = ""
	for _, m := range []model.Message{
		{Role: model.RoleUser, Content: "hi <b>there</b>"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c1", Name: "read", Args: json.RawMessage(`{"path":"a"}`)}}},
		{Role: model.RoleTool, ToolCallID: "c1", Content: "file", IsError: true},
	} {
		if err := store.AppendMessage(id, m); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	for _, r := range []span.Record{
		{ID: "t", Session: id, Kind: span.KindTurn, Name: "hi", Start: start, End: start.Add(3 * time.Second), Status: span.StatusOK, Attrs: map[string]any{}},
		{ID: "r", Parent: "t", Session: id, Kind: span.KindRequest, Start: start, End: start.Add(time.Second), Status: span.StatusOK,
			Attrs: map[string]any{"gen_ai.usage.input_tokens": 100, "wisp.usage.cached_tokens": 60, "gen_ai.usage.output_tokens": 5}},
		{ID: "x", Parent: "t", Session: id, Kind: span.KindTool, Name: "read", Start: start.Add(time.Second), Status: "", Attrs: map[string]any{}}, // still open
		{ID: "a", Parent: "t", Session: id, Kind: span.KindTurn, Start: start, End: start, Status: span.StatusError, Attrs: map[string]any{}},      // a sub-agent's turn
	} {
		if err := store.WriteSpan(r); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(Handler(store, "/here", token))
	defer srv.Close()
	get := func(path string, v any) string {
		t.Helper()
		resp, err := http.Get(srv.URL + "/" + token + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %s", path, resp.Status)
		}
		if v != nil {
			if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
				t.Fatal(err)
			}
		}
		return resp.Header.Get("Content-Type")
	}

	if ct := get("/", nil); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("page content type %q", ct)
	}
	var sessions []sessionJSON
	get("/api/sessions", &sessions)
	byID := map[string]sessionJSON{}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want both directories'", sessions)
	}
	for _, s := range sessions {
		if s.Here != (s.Dir == "/here") || (s.ID == id) != s.Here {
			t.Errorf("session %s in %q: here = %v", s.ID, s.Dir, s.Here)
		}
		byID[s.ID] = s
	}
	tot := byID[id].Totals
	if tot == nil || tot.Spans != 4 || tot.Turns != 1 || tot.Requests != 1 || tot.Tools != 1 || tot.Open != 1 || tot.Errors != 1 ||
		tot.InputTokens != 100 || tot.CachedTokens != 60 || tot.OutputTokens != 5 || byID[id].Preview != "hi <b>there</b>" {
		t.Errorf("session = %+v, totals %+v", byID[id], tot)
	}
	if byID[old].Totals != nil {
		t.Errorf("a session without spans should have no totals: %+v", byID[old].Totals)
	}

	var spans []spanJSON
	get("/api/sessions/"+id+"/spans", &spans)
	if len(spans) != 4 {
		t.Fatalf("spans = %+v", spans)
	}
	for _, s := range spans {
		if s.ID == "x" && s.End != 0 {
			t.Error("an open span should have no end")
		}
		if s.ID == "r" && s.End-s.Start != 1000 {
			t.Errorf("request lasts %v ms, want 1000", s.End-s.Start)
		}
	}

	var msgs []messageJSON
	get("/api/sessions/"+id+"/messages", &msgs)
	if len(msgs) != 3 || msgs[1].ToolCalls[0].Name != "read" || !msgs[2].IsError || msgs[2].ToolCallID != "c1" {
		t.Errorf("messages = %+v", msgs)
	}
}

func TestHandlerLocalOnly(t *testing.T) {
	store, err := session.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	srv := httptest.NewServer(Handler(store, "", token))
	defer srv.Close()
	get := func(header map[string]string) int {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/"+token+"/api/sessions", nil)
		for k, v := range header {
			if k == "Host" {
				req.Host = v
			} else {
				req.Header.Set(k, v)
			}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	// A page from another site, directly or by DNS rebinding, is refused.
	if code := get(map[string]string{"Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Errorf("cross-origin = %d, want 403", code)
	}
	if code := get(map[string]string{"Host": "evil.example:7777"}); code != http.StatusForbidden {
		t.Errorf("rebound host = %d, want 403", code)
	}
	if code := get(map[string]string{"Host": "localhost:7777", "Origin": "http://localhost:7777"}); code != http.StatusOK {
		t.Errorf("localhost = %d, want 200", code)
	}

	// A client on the LAN, e.g. with --trace-addr 0.0.0.0, is refused
	// whatever Host it claims.
	req := httptest.NewRequest("GET", "http://localhost:7777/"+token+"/api/sessions", nil)
	req.RemoteAddr = "192.168.1.20:51000"
	rec := httptest.NewRecorder()
	Handler(store, "", token).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("LAN client = %d, want 403", rec.Code)
	}
}

// Without the secret path nothing is served: another user of the machine
// can reach localhost, but not the transcripts.
func TestHandlerNeedsTheToken(t *testing.T) {
	store, err := session.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	srv := httptest.NewServer(Handler(store, "", token))
	defer srv.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for path, want := range map[string]int{
		"/":                       http.StatusNotFound,
		"/api/sessions":           http.StatusNotFound,
		"/wrong/api/sessions":     http.StatusNotFound,
		"/" + token + "/":         http.StatusOK,
		"/" + token:               http.StatusMovedPermanently, // to the slash, for the page's relative URLs
		"/" + token + "/api/nope": http.StatusNotFound,
	} {
		resp, err := noRedirect.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
		if want == http.StatusOK && resp.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("GET %s: the secret path could leak through a Referer", path)
		}
	}
}
