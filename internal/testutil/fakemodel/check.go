package fakemodel

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Check verifies what every chat request wisp sends must satisfy, so any
// scenario catches history corruption:
//
//   - an optional system message comes first, and only first;
//   - every assistant tool call is answered by exactly one tool message
//     before the next user or assistant message, and every tool message
//     answers such a call;
//   - no two user messages, and no two assistant messages without tool
//     results between them, follow each other;
//   - the tools offered are the same in every request, so the prompt
//     prefix stays cacheable.
func Check(requests []json.RawMessage) error {
	var firstTools string
	for n, raw := range requests {
		var req struct {
			Messages []struct {
				Role       string `json:"role"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					ID string `json:"id"`
				} `json:"tool_calls"`
			} `json:"messages"`
			Tools json.RawMessage `json:"tools"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			return fmt.Errorf("request %d: %w", n+1, err)
		}
		fail := func(i int, format string, args ...any) error {
			return fmt.Errorf("request %d, message %d: %s", n+1, i+1, fmt.Sprintf(format, args...))
		}

		var open []string // tool call ids awaiting results
		prev := ""        // last user or assistant role
		for i, m := range req.Messages {
			switch m.Role {
			case "system":
				if i != 0 {
					return fail(i, "system message after the start")
				}
				continue
			case "tool":
				j := slices.Index(open, m.ToolCallID)
				if j < 0 {
					return fail(i, "tool result %q answers no pending call", m.ToolCallID)
				}
				open = slices.Delete(open, j, j+1)
				prev = "tool" // the assistant may speak again after results
				continue
			case "user", "assistant":
			default:
				return fail(i, "unknown role %q", m.Role)
			}
			if len(open) > 0 {
				return fail(i, "%s message while tool calls %s have no result", m.Role, strings.Join(open, ", "))
			}
			if m.Role == prev {
				return fail(i, "two %s messages in a row", m.Role)
			}
			prev = m.Role
			for _, c := range m.ToolCalls {
				open = append(open, c.ID)
			}
		}
		if len(open) > 0 {
			return fmt.Errorf("request %d ends with tool calls %s unanswered", n+1, strings.Join(open, ", "))
		}

		tools := string(req.Tools)
		if n == 0 {
			firstTools = tools
		} else if tools != firstTools {
			return fmt.Errorf("request %d offers different tools than request 1", n+1)
		}
	}
	return nil
}
