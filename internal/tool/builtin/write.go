package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// WriteTool creates or replaces a whole file, creating missing parent
// directories. The write is atomic (writeFileAtomic).
type WriteTool struct{}

func (WriteTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name:        "write",
		Description: "Create or overwrite a file with the given content.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "file path to write"},
				"content": {"type": "string", "description": "content to write"}
			},
			"required": ["path", "content"]
		}`),
	}
}

func (WriteTool) Risky() bool { return true }

func (WriteTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	if a.Path == "" {
		return tool.Result{}, fmt.Errorf("path is required")
	}
	if a.Content == nil {
		return tool.Result{}, fmt.Errorf("content is required (use an explicit empty string to empty a file)")
	}

	if err := tool.Checkpoint(ctx, a.Path); err != nil {
		return tool.Result{}, fmt.Errorf("saving %s for undo: %w", a.Path, err)
	}
	if dir := filepath.Dir(a.Path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return tool.Result{}, fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if err := writeFileAtomic(a.Path, []byte(*a.Content), 0o644); err != nil {
		return tool.Result{}, fmt.Errorf("writing %s: %w", a.Path, err)
	}

	return tool.Result{Content: fmt.Sprintf("wrote %d bytes to %s", len(*a.Content), a.Path)}, nil
}
