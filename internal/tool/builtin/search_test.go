package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMatchSegments(t *testing.T) {
	for _, c := range []struct {
		pattern, path string
		want          bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "cmd/main.go", false},
		{"**/*.go", "main.go", true},
		{"**/*.go", "cmd/wisp/main.go", true},
		{"cmd/**", "cmd/wisp/main.go", true},
		{"cmd/**/main.go", "cmd/main.go", true},
		{"internal/*/x.go", "internal/a/b/x.go", false},
		{"[", "[", false}, // a malformed pattern matches nothing
	} {
		if got := matchSegments(strings.Split(c.pattern, "/"), strings.Split(c.path, "/")); got != c.want {
			t.Errorf("matchSegments(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// glob and grep stop counting at maxCounted, and say that the list is cut.
func TestSearchStopsAndReportsUnreadable(t *testing.T) {
	defer func(n int) { maxToolResults = n }(maxToolResults)
	maxToolResults = 2
	t.Chdir(t.TempDir())
	for i := range 30 {
		if err := os.WriteFile(fmt.Sprintf("f%02d.txt", i), []byte("hit\nhit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("locked.txt", []byte("hit\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	res, err := GrepTool{}.Run(context.Background(), json.RawMessage(`{"pattern":"hit"}`))
	if err != nil || !strings.Contains(res.Content, "more than 10 matches, search stopped") {
		t.Errorf("grep = %q, %v; want it to stop at 5x the cap", res.Content, err)
	}
	res, err = GrepTool{}.Run(context.Background(), json.RawMessage(`{"pattern":"hit","path":"locked.txt"}`))
	if os.Geteuid() != 0 && (err != nil || !strings.Contains(res.Content, "could not be read")) {
		t.Errorf("unreadable file: %q, %v", res.Content, err)
	}
	res, err = GlobTool{}.Run(context.Background(), json.RawMessage(`{"pattern":"*.txt"}`))
	if err != nil || !strings.Contains(res.Content, "search stopped") || strings.Count(res.Content, "\n") != 2 {
		t.Errorf("glob = %q, %v", res.Content, err)
	}
}
