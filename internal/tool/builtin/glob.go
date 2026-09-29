package builtin

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// GlobTool finds files by name. Results are sorted, so the same call
// gives the same answer.
type GlobTool struct{}

func (GlobTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name:        "glob",
		Description: "Find files matching a glob pattern (supports ** for recursive descent). The pattern is relative to path; results are paths you can pass to other tools. At most 200 are listed.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "glob pattern, e.g. \"**/*.go\""},
				"path": {"type": "string", "description": "base directory to search from (default \".\")"}
			},
			"required": ["pattern"]
		}`),
	}
}

func (GlobTool) Risky() bool { return false }

func (GlobTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	if a.Pattern == "" {
		return tool.Result{}, fmt.Errorf("pattern is required")
	}

	base := cmp.Or(a.Path, ".")
	patSegs := strings.Split(a.Pattern, "/")
	var matches []string
	err = walkFiles(ctx, base, func(p, rel string) bool {
		if matchSegments(patSegs, strings.Split(rel, "/")) {
			matches = append(matches, p)
		}
		return len(matches) < maxCounted()
	})
	if err != nil {
		return tool.Result{}, err
	}
	if len(matches) == 0 {
		return tool.Result{Content: noMatches(a.Pattern, base)}, nil
	}

	sort.Strings(matches)
	total := len(matches)
	matches = matches[:min(total, maxToolResults)]
	return tool.Result{Content: strings.Join(matches, "\n") + resultsNote(total, len(matches))}, nil
}
