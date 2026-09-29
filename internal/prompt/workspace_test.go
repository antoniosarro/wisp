package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// writeFile writes content to dir/name, creating the directories between.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGitBranch(t *testing.T) {
	for _, c := range []struct {
		name   string
		files  map[string]string // relative to the repository root
		sub    string            // where to look from, under the root
		want   string
		wantOK bool
	}{
		{"branch", map[string]string{".git/HEAD": "ref: refs/heads/main\n"}, "", "main", true},
		{"slash in branch", map[string]string{".git/HEAD": "ref: refs/heads/feat/x\n"}, "", "feat/x", true},
		{"from a subdirectory", map[string]string{".git/HEAD": "ref: refs/heads/main\n", "a/b/.keep": ""}, "a/b", "main", true},
		{"detached", map[string]string{".git/HEAD": "0123456789abcdef0123\n"}, "", "detached at 0123456789ab", true},
		{
			"worktree with relative gitdir",
			map[string]string{
				".git/HEAD":              "ref: refs/heads/main\n",
				".git/worktrees/wt/HEAD": "ref: refs/heads/wt-branch\n",
				"wt/.git":                "gitdir: ../.git/worktrees/wt\n",
			},
			"wt", "wt-branch", true,
		},
		{"broken .git stops the walk", map[string]string{".git/HEAD": "ref: refs/heads/main\n", "sub/.git": "gitdir: missing\n"}, "sub", "", false},
		{"no repository", map[string]string{"x/.keep": ""}, "x", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range c.files {
				writeFile(t, root, name, content)
			}
			got, ok := gitBranch(filepath.Join(root, c.sub))
			if got != c.want || ok != c.wantOK {
				t.Errorf("gitBranch = %q, %v; want %q, %v", got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestReadInstructions(t *testing.T) {
	dir := t.TempDir()
	if got := readInstructions(dir); got != "" {
		t.Errorf("without %s = %q, want empty", instructionsFile, got)
	}

	writeFile(t, dir, instructionsFile, "\n  Use tabs.  \n")
	if got := readInstructions(dir); got != "Use tabs." {
		t.Errorf("readInstructions = %q, want it trimmed", got)
	}

	// A 3-byte character straddles the cut, which must keep it whole.
	writeFile(t, dir, instructionsFile, strings.Repeat("a", maxInstructionBytes-1)+"世界")
	got := readInstructions(dir)
	body, ok := strings.CutSuffix(got, "\n... (truncated)")
	if !ok || !utf8.ValidString(got) || len(body) > maxInstructionBytes {
		t.Errorf("long file: %d bytes, valid UTF-8 %v, truncation note %v", len(got), utf8.ValidString(got), ok)
	}
}
