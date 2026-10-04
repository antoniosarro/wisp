package permission

import (
	"cmp"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/antoniosarro/wisp/internal/credential"
)

// Rules remembers calls the user allowed for the rest of the session, by
// RuleKey. Nothing is written to disk. It is safe for concurrent use:
// sub-agents share the main agent's rules.
type Rules struct {
	mu      sync.Mutex
	allowed map[string]bool
}

// allows reports whether a call with key was always-allowed.
func (r *Rules) allows(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.allowed[key]
}

// add always-allows calls with key.
func (r *Rules) add(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.allowed == nil {
		r.allowed = map[string]bool{}
	}
	r.allowed[key] = true
}

// RuleKey identifies which later calls an "always allow" covers; see rule.
func RuleKey(name string, args json.RawMessage) string {
	key, _ := rule(name, args)
	return key
}

// RuleLabel describes RuleKey's scope for approval prompts, e.g. "git
// status commands" or "this file".
func RuleLabel(name string, args json.RawMessage) string {
	_, label := rule(name, args)
	return label
}

// rule returns the scope of an "always allow" and its description:
//   - bash: see bashRule;
//   - fetch: the URL's host;
//   - read, grep (risky only for credentials): the one path asked about;
//   - write, edit, multi_edit: the working directory, except .git and
//     .wisp; a path outside it or in those covers only itself;
//   - any other tool: the whole tool.
func rule(name string, args json.RawMessage) (key, label string) {
	var a struct {
		Command string `json:"command"`
		URL     string `json:"url"`
		Path    string `json:"path"`
	}
	_ = json.Unmarshal(args, &a)
	switch name {
	case "bash":
		return bashRule(a.Command)
	case "fetch":
		host := urlHost(a.URL)
		return "fetch:" + host, host
	case "read", "grep":
		abs, _ := outsideWorkDir(cmp.Or(a.Path, "."))
		return name + "=" + abs, "this file"
	case "write", "edit", "multi_edit":
		if abs, ok := outsideWorkDir(a.Path); ok || controlPath(abs) {
			return name + "=" + abs, "this file"
		}
		return name, name + " in this directory"
	}
	return name, name
}

// programScoped programs only read and report, so approving one call
// covers the program: "ls -la" covers ls commands. Any other program
// covers only the exact command unless it dispatches on a subcommand;
// curl, cp, or tee could otherwise write anywhere once approved.
var programScoped = map[string]bool{
	"ls": true, "wc": true, "pwd": true, "echo": true, "which": true, "file": true, "stat": true,
	"du": true, "df": true, "tr": true, "realpath": true, "readlink": true, "basename": true,
	"dirname": true, "whoami": true, "id": true, "uname": true, "nproc": true, "free": true,
	"uptime": true, "ps": true, "true": true, "false": true,
}

// contentReaders are programScoped too, but print what files contain, so
// their scope covers only calls that can't reach a credential (reachesCredential):
// "always allow cat notes.txt" must not cover "cat ~/.ssh/id_rsa", which
// read would ask about.
var contentReaders = map[string]bool{
	"cat": true, "head": true, "tail": true, "grep": true, "diff": true, "cmp": true, "cut": true, "jq": true,
}

// subcommandPrograms dispatch on their first argument: approving
// "git status" covers git status calls, not git push.
var subcommandPrograms = map[string]bool{
	"git": true, "go": true, "cargo": true, "npm": true, "pnpm": true, "yarn": true, "docker": true,
	"podman": true, "kubectl": true, "gh": true, "just": true, "make": true, "systemctl": true,
	"uv": true, "pip": true, "rustup": true, "brew": true, "apt": true,
}

// exactSubcommands run code or packages named in their arguments, or
// change the tool's own configuration, so even scoped to the subcommand
// one approval would cover anything: "go run ./gen" must not cover
// "go run evil@latest", nor "git config" a hook that runs on commit.
var exactSubcommands = map[string]bool{
	"run": true, "exec": true, "x": true, "dlx": true, "create": true, "init": true, "install": true,
	"i": true, "add": true, "generate": true, "tool": true, "config": true, "env": true, "api": true,
	"eval": true, "shell": true, "cp": true, "attach": true, "debug": true,
}

// shellSyntax is what makes a command more than one simple command:
// separators, pipes, redirects, substitutions, and line breaks.
const shellSyntax = ";&|<>`$()\n\r"

// hasControl reports whether s has a control character other than a tab:
// a simple command has no use for one, and an escape sequence in it could
// redraw the approval prompt that shows it.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r != '\t' && unicode.IsControl(r) })
}

// bashRule scopes a command's "always allow": a simple command of a
// read-only program covers the program, and of a program like git its
// subcommand. Anything else, compound commands included, covers only
// itself, so one approval can't smuggle in more.
func bashRule(command string) (key, label string) {
	key, label = "bash="+command, "this command"
	if strings.ContainsAny(command, shellSyntax) || hasControl(command) {
		return key, label
	}
	fields := strings.Fields(command)
	// A leading VAR= changes what runs, and a path may name a lookalike:
	// ./git isn't git.
	if len(fields) == 0 || strings.ContainsAny(fields[0], "=/") {
		return key, label
	}
	prog := fields[0]
	switch {
	case programScoped[prog], contentReaders[prog] && !reachesCredential(prog, fields[1:]):
		return "bash:" + prog, prog + " commands"
	case subcommandPrograms[prog] && len(fields) > 1 && !strings.HasPrefix(fields[1], "-"):
		// The subcommand is what the shell passes, not how it was written:
		// `go "run"` runs go run, so it must cover only itself like run does.
		if sub, literal := shellArg(fields[1]); literal && !strings.HasPrefix(sub, "-") && !exactSubcommands[sub] {
			scope := prog + " " + sub
			return "bash:" + scope, scope + " commands"
		}
	}
	return key, label
}

// shellArg resolves arg to the word the shell passes to the program,
// removing quotes and backslash escapes and joining the pieces quoting
// split it into: the shell reads `cat .e'nv'` as cat .env. It reports false
// when the word depends on expansion only the shell can resolve, such as a
// brace alternative, a variable, or a command substitution, since then what
// it names can't be known from the text.
func shellArg(arg string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(arg); {
		switch c := arg[i]; c {
		case '\'':
			end := strings.IndexByte(arg[i+1:], '\'')
			if end < 0 {
				return "", false
			}
			b.WriteString(arg[i+1 : i+1+end])
			i += end + 2
		case '"':
			for i++; i < len(arg) && arg[i] != '"'; i++ {
				if arg[i] == '\\' && i+1 < len(arg) && strings.IndexByte("$`\"\\\n", arg[i+1]) >= 0 {
					i++
				}
				b.WriteByte(arg[i])
			}
			if i >= len(arg) {
				return "", false
			}
			i++ // the closing quote
		case '\\':
			if i+1 >= len(arg) {
				return "", false
			}
			b.WriteByte(arg[i+1])
			i += 2
		case '{', '}', '$', '`':
			return "", false
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), true
}

// outsideWorkDir returns path's absolute form, with symlinks resolved,
// and whether it lies outside the working directory. Resolving matters:
// a link inside the directory can point anywhere, and writes follow it.
// A path that can't be resolved counts as outside, so it covers only itself.
func outsideWorkDir(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	wd, wdErr := os.Getwd()
	if err != nil || wdErr != nil {
		return path, true
	}
	abs, wd = resolve(abs), resolve(wd)
	rel, err := filepath.Rel(wd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs, true
	}
	return abs, false
}

// resolve follows the symlinks of path's longest existing prefix, so a
// file yet to be written resolves through its directory.
func resolve(path string) string {
	rest := ""
	for dir := path; ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		if filepath.Dir(dir) == dir {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

// controlPath says whether path, absolute and resolved, is in one of the
// working directory's directories whose files run code or configure wisp:
// .git (hooks, config) or .wisp (MCP servers, agents), at any depth.
// An "always allow" for writes in the directory doesn't cover them.
func controlPath(path string) bool {
	wd, err := os.Getwd()
	if err != nil {
		return true
	}
	rel, err := filepath.Rel(resolve(wd), path)
	if err != nil {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".git" || part == ".wisp" {
			return true
		}
	}
	return false
}

// urlHost is the host (and port) raw names, lower-cased, or raw itself
// when it doesn't parse as a URL with a host.
func urlHost(raw string) string {
	// Parsed, not cut: in https://docs.x@evil.com/ the host is evil.com.
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return strings.ToLower(u.Host)
}

// globChars make an argument a pattern: what it matches isn't known until
// the shell expands it.
const globChars = "*?["

// reachesCredential reports whether a content reader's arguments could
// make it print a credential: an argument naming one (credential.Path,
// with ~ expanded and --opt=value values checked), a glob, a directory,
// or, for grep, a recursive search, which with no path searches ".".
func reachesCredential(prog string, args []string) bool {
	// grep's and jq's first operand is the pattern or filter, not a file;
	// grep takes it from -e or -f instead when either is given.
	skipOperand := prog == "jq" || prog == "grep" && !slices.ContainsFunc(args, func(a string) bool {
		return a == "-e" || a == "-f" || strings.HasPrefix(a, "--regexp") || strings.HasPrefix(a, "--file")
	})
	skipNext := false
	for _, arg := range args {
		if skipNext { // grep -e's pattern
			skipNext = false
			continue
		}
		if prog == "grep" && recursiveGrep(arg) {
			return true
		}
		if prog == "grep" && (arg == "-e" || arg == "--regexp") {
			skipNext = true
			continue
		}
		if _, value, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(arg, "-") {
			arg = value
		} else if strings.HasPrefix(arg, "-") {
			continue
		} else if skipOperand {
			skipOperand = false
			continue
		}
		// Resolve the word the shell passes: a quoted or escaped name can
		// still be a credential. A word that needs expansion names nothing
		// knowable, so it is treated as reaching one.
		word, literal := shellArg(arg)
		if !literal {
			return true
		}
		word = expandHome(word)
		if word == "" {
			continue
		}
		if strings.ContainsAny(word, globChars) || credential.Path(word) {
			return true
		}
		if info, err := os.Stat(word); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// recursiveGrep reports whether arg is one of grep's recursive options:
// -r or -R, alone or in a cluster such as -rn, or their long forms.
func recursiveGrep(arg string) bool {
	if long, ok := strings.CutPrefix(arg, "--"); ok {
		return strings.HasPrefix(long, "recursive") || strings.HasPrefix(long, "dereference-recursive") || strings.HasPrefix(long, "directories")
	}
	return strings.HasPrefix(arg, "-") && strings.ContainsAny(arg[1:], "rRd")
}

// expandHome expands a leading ~ the way the shell will.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return home + path[1:]
}
