package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// TodoTool lets the model keep a visible task list. It is stateless: each
// call sends the whole list, so the plan lives in the conversation.
type TodoTool struct{}

var todoStatuses = map[string]bool{"pending": true, "in_progress": true, "completed": true}

// todoArgs is the todo tool's arguments: the whole list, every call.
type todoArgs struct {
	Todos []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"todos"`
}

func (TodoTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name:        "todo",
		Description: "Write the current task list for multi-step work. Send the full list every time. Keep exactly one task in_progress while working and mark tasks completed as soon as they are done.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"todos": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {
							"content": {"type": "string", "description": "what the task is"},
							"status": {"type": "string", "enum": ["pending", "in_progress", "completed"]}
						},
						"required": ["content", "status"]
					}
				}
			},
			"required": ["todos"]
		}`),
	}
}

func (TodoTool) Risky() bool { return false }

func (TodoTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[todoArgs](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	if len(a.Todos) == 0 {
		return tool.Result{Content: "(no tasks)"}, nil
	}
	// The list is already in the call's arguments; echoing it back would
	// cost its tokens twice for the rest of the session.
	left, active := 0, 0
	for i, t := range a.Todos {
		if _, ok := todoStatuses[t.Status]; !ok {
			return tool.Result{}, fmt.Errorf("task %d: status must be pending, in_progress or completed", i+1)
		}
		if strings.TrimSpace(t.Content) == "" {
			return tool.Result{}, fmt.Errorf("task %d: content is empty", i+1)
		}
		if t.Status != "completed" {
			left++
		}
		if t.Status == "in_progress" {
			active++
		}
	}
	if left == 0 {
		return tool.Result{Content: fmt.Sprintf("Task list updated: all %d task(s) completed.", len(a.Todos))}, nil
	}
	msg := fmt.Sprintf("Task list updated: %d of %d task(s) left. Mark each one completed as soon as it is done.", left, len(a.Todos))
	if active > 1 {
		// Accepted anyway: rejecting the list would only cost a retry.
		msg += fmt.Sprintf(" Note: %d tasks are in_progress; keep exactly one in_progress.", active)
	}
	return tool.Result{Content: msg}, nil
}
