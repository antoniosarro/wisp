package builtin

import (
	"context"
	"encoding/json"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// MultiEditTool applies several edits to one file, all or nothing, so a
// change spanning several places can't be left half done.
type MultiEditTool struct{}

func (MultiEditTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name:        "multi_edit",
		Description: "Apply several exact-string replacements to one file, in order. Each edit sees the result of the previous one; if any edit fails, nothing is written.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "file path to edit"},
				"edits": {
					"type": "array",
					"description": "replacements to apply in order",
					"items": {
						"type": "object",
						"properties": {
							"old_string": {"type": "string", "description": "exact text to find"},
							"new_string": {"type": "string", "description": "text to replace it with"},
							"replace_all": {"type": "boolean", "description": "replace every occurrence instead of requiring exactly one"}
						},
						"required": ["old_string", "new_string"]
					}
				}
			},
			"required": ["path", "edits"]
		}`),
	}
}

func (MultiEditTool) Risky() bool { return true }

func (MultiEditTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[struct {
		Path  string        `json:"path"`
		Edits []replacement `json:"edits"`
	}](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	return editFile(ctx, a.Path, a.Edits)
}
