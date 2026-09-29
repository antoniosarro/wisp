package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestGlobTool(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir,
		"a.go",
		"main.go",
		"internal/tool/read.go",
		"internal/tool/read_test.go",
		".git/HEAD",
	)

	tests := []struct {
		name, path string
		pattern    string
		want       []string
	}{
		{name: "top-level only", pattern: "*.go", want: []string{"a.go", "main.go"}},
		{name: "recursive", pattern: "**/*.go", want: []string{"a.go", "internal/tool/read.go", "internal/tool/read_test.go", "main.go"}},
		{name: "recursive scoped to a dir", pattern: "internal/**/*.go", want: []string{"internal/tool/read.go", "internal/tool/read_test.go"}},
		{name: "results usable from the working directory", path: "internal", pattern: "**/*.go", want: []string{"internal/tool/read.go", "internal/tool/read_test.go"}},
	}

	t.Chdir(dir)
	var gt GlobTool
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := json.RawMessage(fmt.Sprintf(`{"pattern":%q,"path":%q}`, tt.pattern, tt.path))
			res, err := gt.Run(context.Background(), args)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := strings.Split(res.Content, "\n")
			if !equalUnordered(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGlobToolNoMatches(t *testing.T) {
	res, err := GlobTool{}.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"pattern":"*.rs","path":%q}`, t.TempDir())))
	if err != nil || !strings.HasPrefix(res.Content, "(no matches") {
		t.Fatalf("Run() = %q, %v; want an explicit no-matches note", res.Content, err)
	}
}

func TestGlobToolSkipsGitDir(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, ".git/config")

	var gt GlobTool
	res, err := gt.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"pattern":"**/*","path":%q}`, dir)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(res.Content, ".git") {
		t.Errorf("Content = %q, want .git contents excluded", res.Content)
	}
}
