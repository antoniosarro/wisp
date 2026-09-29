package builtin

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// GrepTool searches file contents with Go's RE2 regular expressions, so
// no external grep or ripgrep is needed.
type GrepTool struct{}

// maxGrepLineBytes caps each reported match line; longer lines show the
// part around the match.
const maxGrepLineBytes = 300

func (GrepTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name:        "grep",
		Description: "Search file contents for a regular expression (Go RE2 syntax; prefix (?i) for case-insensitive). Returns path:line:text matches, at most 200.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "regular expression to search for"},
				"path": {"type": "string", "description": "directory or file to search (default \".\")"},
				"glob": {"type": "string", "description": "optional glob, relative to path, to filter which files are searched, e.g. \"**/*.go\""},
				"files_only": {"type": "boolean", "description": "list only the paths of files with matches"}
			},
			"required": ["pattern"]
		}`),
	}
}

func (GrepTool) Risky() bool { return false }

// RiskyCall asks before searching credentials; a wider search skips them.
func (GrepTool) RiskyCall(args json.RawMessage) bool { return sensitivePath(pathArg(args)) }

func (GrepTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[struct {
		Pattern   string `json:"pattern"`
		Path      string `json:"path"`
		Glob      string `json:"glob"`
		FilesOnly bool   `json:"files_only"`
	}](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	if a.Pattern == "" {
		return tool.Result{}, fmt.Errorf("pattern is required")
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return tool.Result{}, fmt.Errorf("compiling pattern: %w", err)
	}
	var globSegs []string
	if a.Glob != "" {
		globSegs = strings.Split(a.Glob, "/")
	}

	base := cmp.Or(a.Path, ".")
	g := grepSearch{re: re, filesOnly: a.FilesOnly}
	err = walkFiles(ctx, base, func(p, rel string) bool {
		if globSegs != nil && !matchSegments(globSegs, strings.Split(rel, "/")) {
			return true
		}
		// A credential file named as the path was approved (RiskyCall);
		// one met on the way is not searched.
		if p != filepath.ToSlash(base) && credentialFile(p) {
			g.credentials++
			return true
		}
		g.file(ctx, p)
		return g.total < maxCounted()
	})
	if err != nil {
		return tool.Result{}, err
	}
	content := strings.Join(g.matches, "\n") + resultsNote(g.total, len(g.matches))
	if g.total == 0 {
		content = noMatches(a.Pattern, base)
	}
	if g.unreadable > 0 {
		content += fmt.Sprintf("\n... (%d file(s) could not be read to the end, so results may be incomplete)", g.unreadable)
	}
	if g.credentials > 0 {
		content += fmt.Sprintf("\n... (%d credential file(s), such as .env or keys, were not searched; grep one by path to search it, which asks the user first)", g.credentials)
	}
	return tool.Result{Content: content}, nil
}

// grepSearch collects matches across files.
type grepSearch struct {
	re          *regexp.Regexp
	filesOnly   bool
	matches     []string // "path:line:text", or paths when filesOnly
	total       int      // matches (or files) found, counted up to maxCounted
	unreadable  int      // files that failed to open or read to the end
	credentials int      // credential files skipped (credentialFile)
}

// file adds the matches in the file at p. Binary files are skipped.
func (g *grepSearch) file(ctx context.Context, p string) {
	// Only regular files: a FIFO or device in the tree would block the walk.
	if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
		return
	}
	f, err := os.Open(p)
	if err != nil {
		g.unreadable++
		return
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, sniffBytes)
	n, _ := f.Read(head)
	if isBinary(head[:n]) {
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		g.unreadable++
		return
	}

	lineNum := 0
	err = forEachLine(f, func(line string) bool {
		lineNum++
		loc := g.re.FindStringIndex(line)
		if loc == nil {
			return ctx.Err() == nil
		}
		g.total++
		if g.filesOnly {
			if len(g.matches) < maxToolResults {
				g.matches = append(g.matches, p)
			}
			return false
		}
		if len(g.matches) < maxToolResults {
			g.matches = append(g.matches, fmt.Sprintf("%s:%d:%s", p, lineNum, snippet(line, loc[0], maxGrepLineBytes)))
		}
		return ctx.Err() == nil && g.total < maxCounted()
	})
	if err != nil {
		g.unreadable++
	}
}
