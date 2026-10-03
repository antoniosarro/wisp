package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// EditTool replaces exact strings in a file. Exact matching, not fuzzy,
// so an edit never lands somewhere the model didn't mean; when it fails,
// notFoundHint says why.
type EditTool struct{}

func (EditTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name:        "edit",
		Description: "Replace an exact string in a file. old_string must match exactly once unless replace_all is set.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "file path to edit"},
				"old_string": {"type": "string", "description": "exact text to find"},
				"new_string": {"type": "string", "description": "text to replace it with"},
				"replace_all": {"type": "boolean", "description": "replace every occurrence instead of requiring exactly one"}
			},
			"required": ["path", "old_string", "new_string"]
		}`),
	}
}

func (EditTool) Risky() bool { return true }

func (EditTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[struct {
		Path string `json:"path"`
		replacement
	}](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	return editFile(ctx, a.Path, []replacement{a.replacement})
}

// replacement is one exact-string substitution.
type replacement struct {
	OldString  string  `json:"old_string"`
	NewString  *string `json:"new_string"`
	ReplaceAll bool    `json:"replace_all"`
}

// apply substitutes r in content, returning how many occurrences it replaced.
func (r replacement) apply(content string) (string, int, error) {
	switch {
	case r.OldString == "":
		return "", 0, fmt.Errorf("old_string is required")
	case r.NewString == nil:
		return "", 0, fmt.Errorf("new_string is required (use an explicit empty string to delete text)")
	case r.OldString == *r.NewString:
		return "", 0, fmt.Errorf("old_string and new_string are identical")
	}
	n := strings.Count(content, r.OldString)
	switch {
	case n == 0:
		return "", 0, fmt.Errorf("old_string not found: %s", notFoundHint(content, r.OldString))
	case n > 1 && !r.ReplaceAll:
		return "", 0, fmt.Errorf("old_string matches %d times, expected exactly 1 (set replace_all to replace all of them)", n)
	}
	if !r.ReplaceAll {
		n = 1
	}
	return strings.Replace(content, r.OldString, *r.NewString, n), n, nil
}

// withCRLF adapts an LF-only replacement to a file with CRLF line endings.
func (r replacement) withCRLF() replacement {
	if strings.Contains(r.OldString, "\r") {
		return r
	}
	r.OldString = strings.ReplaceAll(r.OldString, "\n", "\r\n")
	if r.NewString != nil {
		s := strings.ReplaceAll(*r.NewString, "\n", "\r\n")
		r.NewString = &s
	}
	return r
}

// lineNumberPrefix is read's line prefix, "     12\t", copied into an edit.
var lineNumberPrefix = regexp.MustCompile(`(?m)^\s*\d+\t`)

// notFoundHint guesses why old_string didn't match, so the next attempt
// can fix it instead of retrying blindly.
func notFoundHint(content, old string) string {
	if lineNumberPrefix.MatchString(old) {
		return "it contains line-number prefixes from read output; remove them"
	}
	if strings.Contains(squashSpace(content), squashSpace(old)) {
		return "it matches only when whitespace is ignored; copy the exact indentation and spacing from the file"
	}
	// The first line long enough to be distinctive shows where the model
	// meant, if it is in the file.
	for _, line := range strings.Split(old, "\n") {
		if line = strings.TrimSpace(line); len(line) < 8 {
			continue
		}
		if i := strings.Index(content, line); i >= 0 {
			return fmt.Sprintf("its line %q is at line %d of the file, but the surrounding text differs; read that part again and copy it exactly", line, strings.Count(content[:i], "\n")+1)
		}
		break
	}
	return "read the file again; it may differ from what you expect"
}

// squashSpace trims each line and drops blank ones.
func squashSpace(s string) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, strings.Join(strings.Fields(l), " "))
		}
	}
	return strings.Join(lines, "\n")
}

// editFile applies edits in order and writes the file only if all succeed.
// In a CRLF file, an edit written with LF line endings is converted.
func editFile(ctx context.Context, path string, edits []replacement) (tool.Result, error) {
	if path == "" {
		return tool.Result{}, fmt.Errorf("path is required")
	}
	if len(edits) == 0 {
		return tool.Result{}, fmt.Errorf("edits is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return tool.Result{}, fmt.Errorf("reading %s: %w", path, err)
	}

	content, total := string(raw), 0
	crlf := strings.Contains(content, "\r\n")
	for i, e := range edits {
		// Only when the text as given is absent: in a file with mixed line
		// endings, LF-only regions still match as they are.
		if crlf && !strings.Contains(content, e.OldString) {
			e = e.withCRLF()
		}
		var n int
		if content, n, err = e.apply(content); err != nil {
			if len(edits) > 1 {
				return tool.Result{}, fmt.Errorf("edit %d in %s: %w (no changes written)", i+1, path, err)
			}
			return tool.Result{}, fmt.Errorf("%s: %w", path, err)
		}
		total += n
	}

	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if err := tool.Checkpoint(ctx, path); err != nil {
		return tool.Result{}, fmt.Errorf("saving %s for undo: %w", path, err)
	}
	// The file exists, so it keeps its mode; the perm argument is unused.
	if err := writeFileAtomic(path, []byte(content), 0o644); err != nil {
		return tool.Result{}, fmt.Errorf("writing %s: %w", path, err)
	}
	return tool.Result{Content: fmt.Sprintf("replaced %d occurrence(s) in %s", total, path)}, nil
}
