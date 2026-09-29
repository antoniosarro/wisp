package permission

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashRuleScopes(t *testing.T) {
	for cmd, want := range map[string]string{
		"git status":           "git status commands",
		"git -C x push":        "this command",
		"ls -la":               "ls commands",
		"git status | head":    "this command",
		"echo $(whoami)":       "this command",
		"python3 -c 'x'":       "this command",
		"find . -delete":       "this command",
		"FOO=1 ls":             "this command",
		"nix develop -c go":    "this command",
		"/usr/bin/env ls -l":   "this command",
		"/usr/bin/git status":  "this command",
		"./git status":         "this command",
		"curl https://docs.x":  "this command",
		"cp a b":               "this command",
		"go run ./gen":         "this command",
		"npm install left-pad": "this command",
		"git config core.x y":  "this command",
		"go test ./...":        "go test commands",
		"ls\r":                 "this command",
		"git status\x1b[2K":    "this command", // control characters: only itself
		"ls\t-la":              "ls commands",
		"":                     "this command",
	} {
		if got := RuleLabel("bash", commandArgs(cmd)); got != want {
			t.Errorf("RuleLabel(bash, %q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestRuleLabels(t *testing.T) {
	if got := RuleLabel("edit", json.RawMessage(`{"path":"a/b.go"}`)); got != "edit in this directory" {
		t.Errorf("edit label = %q", got)
	}
	if key := RuleKey("write", json.RawMessage(`{"path":"/no-such-wisp-dir/hosts"}`)); key != "write=/no-such-wisp-dir/hosts" {
		t.Errorf("write outside the working directory: key = %q, want only that file", key)
	}
	if got := RuleLabel("todo", nil); got != "todo" {
		t.Errorf("todo label = %q", got)
	}
}

func TestFetchRuleHost(t *testing.T) {
	for raw, want := range map[string]string{
		"https://docs.x@evil.com/p": "evil.com",
		"https://Go.dev/doc?x=1":    "go.dev",
		"http://localhost:8000/v1":  "localhost:8000",
		"not a url":                 "not a url",
	} {
		if got := RuleLabel("fetch", json.RawMessage(`{"url":"`+raw+`"}`)); got != want {
			t.Errorf("fetch rule for %s = %q, want %q", raw, got, want)
		}
	}
}

func TestFileRulesResolveLinksAndGuardControlDirs(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	outside, _ = filepath.EvalSymlinks(outside)
	t.Chdir(dir)
	if err := os.Symlink(outside, filepath.Join(dir, "cfg")); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"src/main.go":           "write",
		"cfg/fish/config.fish":  "write=" + filepath.Join(outside, "fish", "config.fish"),
		".git/hooks/pre-commit": "write=",
		".wisp/mcp.json":        "write=",
		"sub/.git/config":       "write=", // a submodule's .git too
		"docs/.gitignore":       "write",
	} {
		key := RuleKey("write", json.RawMessage(`{"path":"`+path+`"}`))
		if want == "write=" && !strings.HasPrefix(key, want) || want != "write=" && key != want {
			t.Errorf("RuleKey(write, %s) = %q, want %q", path, key, want)
		}
	}
}

func TestAlwaysAllowCoversMatchingCallsOnly(t *testing.T) {
	spy := &spyPrompter{always: true}
	gated := GateAll(spy, bashLike{})
	_, _ = gated[0].Run(context.Background(), commandArgs("go test ./..."))
	asks := func(cmd string) bool {
		spy.called, spy.always = false, false
		_, _ = gated[0].Run(context.Background(), commandArgs(cmd))
		return spy.called
	}
	if asks("go test -run X ./internal/...") {
		t.Error("simple command of an always-allowed subcommand prompted again")
	}
	if !asks("go vet ./...") {
		t.Error("a different subcommand reused the rule")
	}
	if !asks("go test && rm -rf /") {
		t.Error("compound command reused a program rule")
	}
	if !asks("make") {
		t.Error("different program was allowed without asking")
	}
}

// Programs that print file contents keep their program-wide scope only for
// calls that can't reach a credential: "always allow cat notes.txt" must
// not cover "cat ~/.ssh/id_rsa", which read would ask about.
func TestContentReadersDontCoverCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	for _, f := range []string{"notes.txt", "data.json", "main.go"} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir("src", 0o755); err != nil {
		t.Fatal(err)
	}
	for cmd, want := range map[string]string{
		"cat notes.txt":                "cat commands",
		"head -n 5 notes.txt":          "head commands",
		"grep -n TODO main.go":         "grep commands",
		"grep -e src main.go":          "grep commands", // src is the pattern given with -e... and main.go a file
		"jq . data.json":               "jq commands",   // "." is jq's filter, not the directory
		"diff notes.txt main.go":       "diff commands",
		"cat ~/.ssh/id_rsa":            "this command",
		"cat .env":                     "this command",
		"tail --file=.env.local":       "this command", // an option's value is a path too
		"cat '.env'":                   "this command", // quotes don't hide it
		"cat *.txt":                    "this command", // a glob could match anything
		"cat ~":                        "this command", // a directory
		"grep TODO src":                "this command",
		"grep -rn KEY":                 "this command", // searches "." recursively
		"grep --recursive KEY main.go": "this command",
		"grep KEY -R":                  "this command",
		"jq . ~/.docker/config.json":   "this command",
		"ls ~/.ssh":                    "ls commands", // lists names, prints no contents
	} {
		if got := RuleLabel("bash", commandArgs(cmd)); got != want {
			t.Errorf("RuleLabel(bash, %q) = %q, want %q", cmd, got, want)
		}
	}
}
