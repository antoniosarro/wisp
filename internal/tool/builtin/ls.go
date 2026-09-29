package builtin

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/textfmt"
	"github.com/antoniosarro/wisp/internal/tool"
)

// LsTool lists one directory. Symlinks end in "@" and are not followed.
type LsTool struct{}

func (LsTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name:        "ls",
		Description: "List a directory: subdirectories first (ending in /), then files with their sizes. At most 200 entries are listed.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "directory to list (default \".\")"}
			}
		}`),
	}
}

func (LsTool) Risky() bool { return false }

func (LsTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[struct {
		Path string `json:"path"`
	}](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	dir := cmp.Or(a.Path, ".")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return tool.Result{}, fmt.Errorf("listing %s: %w", dir, err)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].IsDir() && !entries[j].IsDir() })
	if len(entries) == 0 {
		return tool.Result{Content: "(empty directory)"}, nil
	}

	lines := make([]string, 0, min(len(entries), maxToolResults))
	for _, e := range entries[:min(len(entries), maxToolResults)] {
		switch info, err := e.Info(); {
		case e.IsDir():
			lines = append(lines, e.Name()+"/")
		case err != nil:
			lines = append(lines, e.Name())
		case info.Mode()&os.ModeSymlink != 0:
			lines = append(lines, e.Name()+"@")
		default:
			lines = append(lines, e.Name()+"\t"+textfmt.Size(info.Size()))
		}
	}
	return tool.Result{Content: strings.Join(lines, "\n") + truncationNote(len(entries), len(lines))}, nil
}
