package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolOutputLayouts(t *testing.T) {
	cases := []struct {
		name, args, result string
		failed             bool
		want               []string
	}{
		{"glob", `{"pattern":"*"}`, "a.go\nb.go\n... (3 more, truncated at 2)", false, []string{"•  a.go", "•  b.go", "... (3 more"}},
		{"grep", `{"pattern":"x"}`, "a.go:3:x := 1\na.go:9:y := x\nb.go:1:x", false, []string{"a.go", "   3 │ x := 1", "   9 │ y := x", "b.go"}},
		{"read", `{"path":"main.go"}`, "     1\tpackage main\n     2\t\n... (file continues; read again with offset=2)", false, []string{"main.go", "1  package main", "file continues"}},
		{"bash", `{"command":"make"}`, "built\n[stderr]\nwarning\n[exit code 2]", true, []string{"$ make", "built", "stderr", "warning", "exit code 2"}},
		{"edit", `{}`, "old_string not found", true, []string{"old_string not found"}},
	}
	for _, c := range cases {
		got := stripANSI(renderToolOutput(c.name, json.RawMessage(c.args), c.result, c.failed, 60))
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s output missing %q:\n%s", c.name, w, got)
			}
		}
	}
	if got := stripANSI(renderToolOutput("grep", nil, "a.go:3:x\na.go:4:y", false, 60)); strings.Count(got, "a.go") != 1 {
		t.Errorf("grep matches not grouped by file:\n%s", got)
	}
}

func TestNewToolViews(t *testing.T) {
	cases := []struct {
		name, args, result string
		want               []string
	}{
		{"ls", `{}`, "src/\ngo.mod\t1.2 KB", []string{"src/", "go.mod  1.2 KB"}},
		{"todo", `{"todos":[{"content":"plan","status":"completed"},{"content":"build","status":"in_progress"},{"content":"test","status":"pending"}]}`, "Task list updated", []string{"✓ plan", "◐ build", "○ test"}},
		{"fetch", `{"url":"https://go.dev"}`, "# Go\nfast", []string{"https://go.dev", "# Go"}},
	}
	for _, c := range cases {
		got := stripANSI(renderToolOutput(c.name, json.RawMessage(c.args), c.result, false, 60))
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s view missing %q:\n%s", c.name, w, got)
			}
		}
	}
	if got := toolDetail("todo", json.RawMessage(`{"todos":[{"status":"completed"},{"status":"pending"}]}`)); got != "1/2 done" {
		t.Errorf("todo detail = %q", got)
	}
}
