package fakemodel

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func post(t *testing.T, url, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(url+"/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRulesReactToTheLastMessage(t *testing.T) {
	s := Start(Script{Rules: []Rule{
		{When: When{Role: "user"}, Reply: Reply{ToolCalls: []ToolCall{{Name: "bash", Args: map[string]any{"command": "ls"}}}}},
		{When: When{Contains: "FAIL"}, Reply: Reply{Status: 429, Error: "slow down"}},
		{When: When{Contains: "FAIL"}, Reply: Reply{Text: "héllo wörld", Chunk: 2}},
	}}, "")
	defer s.Close()

	code, body := post(t, s.URL, `{"messages":[{"role":"user","content":"go"}]}`)
	if code != 200 || !strings.Contains(body, `"name":"bash"`) || !strings.Contains(body, `"finish_reason":"tool_calls"`) || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("first reply %d: %s", code, body)
	}
	if code, _ := post(t, s.URL, `{"messages":[{"role":"tool","content":"FAIL"}]}`); code != 429 {
		t.Fatalf("second reply status %d, want the 429 rule", code)
	}
	_, body = post(t, s.URL, `{"messages":[{"role":"tool","content":"FAIL"}]}`)
	var text strings.Builder
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct{ Content string } `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("bad chunk %q: %v", data, err)
		}
		for _, c := range chunk.Choices {
			text.WriteString(c.Delta.Content)
		}
	}
	if text.String() != "héllo wörld" {
		t.Fatalf("streamed text = %q", text.String())
	}
	if code, body := post(t, s.URL, `{"messages":[{"role":"user","content":"again"}]}`); code != 500 || !strings.Contains(body, "no rule matches") {
		t.Fatalf("unmatched request: %d %s", code, body)
	}
	if n := len(s.Requests()); n != 4 {
		t.Fatalf("recorded %d requests, want 4", n)
	}
}

func TestCheck(t *testing.T) {
	good := `{"messages":[{"role":"system"},{"role":"user"},{"role":"assistant","tool_calls":[{"id":"a"},{"id":"b"}]},{"role":"tool","tool_call_id":"b"},{"role":"tool","tool_call_id":"a"},{"role":"assistant"},{"role":"user"}],"tools":[1]}`
	if err := Check([]json.RawMessage{json.RawMessage(good), json.RawMessage(good)}); err != nil {
		t.Fatalf("valid history rejected: %v", err)
	}
	for name, bad := range map[string]string{
		"unanswered call": `{"messages":[{"role":"user"},{"role":"assistant","tool_calls":[{"id":"a"}]},{"role":"user"}]}`,
		"orphan result":   `{"messages":[{"role":"user"},{"role":"tool","tool_call_id":"x"}]}`,
		"two users":       `{"messages":[{"role":"user"},{"role":"user"}]}`,
		"two assistants":  `{"messages":[{"role":"user"},{"role":"assistant"},{"role":"assistant"}]}`,
		"late system":     `{"messages":[{"role":"user"},{"role":"system"}]}`,
	} {
		if Check([]json.RawMessage{json.RawMessage(bad)}) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	changed := strings.Replace(good, `"tools":[1]`, `"tools":[2]`, 1)
	if Check([]json.RawMessage{json.RawMessage(good), json.RawMessage(changed)}) == nil {
		t.Error("changing tools between requests accepted")
	}
}

func TestChunksKeepCharactersWhole(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want []string
	}{
		{"", 3, nil},
		{"abcdef", 0, []string{"abcdef"}},
		{"abcdef", 4, []string{"abcd", "ef"}},
		{"héllo", 2, []string{"hé", "ll", "o"}}, // é is 2 bytes: the cut moves past it
	} {
		got := chunks(c.s, c.n)
		if strings.Join(got, "|") != strings.Join(c.want, "|") || len(got) != len(c.want) {
			t.Errorf("chunks(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestLoadAndRoutes(t *testing.T) {
	path := t.TempDir() + "/model.yaml"
	if err := os.WriteFile(path, []byte("routes:\n  GET /props: {n_ctx: 4096}\nrules:\n  - reply: {text: hi, delay: 1ms}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script, err := Load(path)
	if err != nil || len(script.Rules) != 1 || script.Rules[0].Reply.Delay != Duration(time.Millisecond) {
		t.Fatalf("Load = %+v, %v", script, err)
	}
	s := Start(script, "")
	defer s.Close()
	for path, want := range map[string]string{"/props": `{"n_ctx":4096}`, "/v1/models": `"id":"fake"`} {
		resp, err := http.Get(strings.TrimSuffix(s.URL, "/v1") + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(string(b), want) {
			t.Errorf("GET %s = %s, want %s", path, b, want)
		}
	}
	if resp, err := http.Get(strings.TrimSuffix(s.URL, "/v1") + "/api/ps"); err != nil || resp.StatusCode != 404 {
		t.Errorf("unlisted route: %v, %v; want 404", resp, err)
	}
}
