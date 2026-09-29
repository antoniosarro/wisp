package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func TestTodo(t *testing.T) {
	for _, c := range []struct {
		name, args, want string
	}{
		{"tasks left", `{"todos":[{"content":"plan","status":"completed"},{"content":"build","status":"in_progress"},{"content":"test","status":"pending"}]}`,
			"Task list updated: 2 of 3 task(s) left. Mark each one completed as soon as it is done."},
		{"all done", `{"todos":[{"content":"plan","status":"completed"}]}`, "Task list updated: all 1 task(s) completed."},
		{"empty list", `{"todos":[]}`, "(no tasks)"},
		// Accepted with a note: rejecting the list would only cost a retry.
		{"two in progress", `{"todos":[{"content":"a","status":"in_progress"},{"content":"b","status":"in_progress"}]}`,
			"Task list updated: 2 of 2 task(s) left. Mark each one completed as soon as it is done. Note: 2 tasks are in_progress; keep exactly one in_progress."},
	} {
		if got := run(t, TodoTool{}, c.args).Content; got != c.want {
			t.Errorf("%s: Content = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTodoRejectsInvalidTasks(t *testing.T) {
	for _, args := range []string{
		`{"todos":[{"content":"x","status":"done"}]}`,
		`{"todos":[{"content":" ","status":"pending"}]}`,
	} {
		if runErr(TodoTool{}, args) == nil {
			t.Errorf("%s accepted", args)
		}
	}
}

func TestTodoReminder(t *testing.T) {
	call := func(args string) model.Message {
		return model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{Name: "todo", Args: json.RawMessage(args)}}}
	}
	open := call(`{"todos":[{"content":"a","status":"completed"},{"content":"b","status":"pending"},{"content":"c","status":"in_progress"}]}`)
	done := call(`{"todos":[{"content":"a","status":"completed"}]}`)

	if got := TodoReminder([]model.Message{open}); !strings.Contains(got, "b; c") {
		t.Errorf("reminder = %q, want the unfinished tasks listed", got)
	}
	if got := TodoReminder([]model.Message{open, done}); got != "" {
		t.Errorf("reminder after completing the list = %q, want none", got)
	}
	if got := TodoReminder([]model.Message{{Role: model.RoleUser, Content: "hi"}}); got != "" {
		t.Errorf("reminder without a todo list = %q, want none", got)
	}
}
