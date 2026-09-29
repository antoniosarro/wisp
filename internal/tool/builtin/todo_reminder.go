package builtin

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
)

// TodoReminder is a core.Loop FinishCheck: if the turn's latest todo list
// still has unfinished tasks, it asks the model to update or finish them.
func TodoReminder(turn []model.Message) string {
	open := unfinishedTodos(turn)
	if len(open) == 0 {
		return ""
	}
	return fmt.Sprintf("Your task list still has unfinished tasks: %s. If they are done, call the todo tool to mark them completed; otherwise keep working on them. If you are waiting for the user's input, say so and stop.",
		strings.Join(open, "; "))
}

// unfinishedTodos returns the tasks not completed in the latest todo call,
// which holds the whole list.
func unfinishedTodos(messages []model.Message) []string {
	for i := len(messages) - 1; i >= 0; i-- {
		calls := messages[i].ToolCalls
		for j := len(calls) - 1; j >= 0; j-- {
			if calls[j].Name != "todo" {
				continue
			}
			var a todoArgs
			if json.Unmarshal(calls[j].Args, &a) != nil {
				return nil
			}
			var open []string
			for _, t := range a.Todos {
				if t.Status != "completed" {
					open = append(open, t.Content)
				}
			}
			return open
		}
	}
	return nil
}
