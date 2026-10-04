package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

// A session's messages are stored in append order; masking and compactions
// refer to them by index in that order, which the store never changes.

// nullable stores "" as NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// encodeCalls encodes tool calls for the tool_calls column, leaving <, >,
// and & unescaped so arguments come back byte for byte as the model wrote
// them.
func encodeCalls(calls []model.ToolCall) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(calls); err != nil {
		return "", fmt.Errorf("encoding tool calls: %w", err)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// AppendMessage appends one message to a session's history. Images aren't
// stored: a resumed session reads the file again if it needs one.
func (s *Store) AppendMessage(sessionID string, msg model.Message) error {
	if err := s.materialize(sessionID); err != nil {
		return err
	}
	var toolCallsJSON any
	if len(msg.ToolCalls) > 0 {
		b, err := encodeCalls(msg.ToolCalls)
		if err != nil {
			return err
		}
		toolCallsJSON = b
	}
	_, err := s.db.Exec(
		`INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, is_error, elided, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, string(msg.Role), msg.Content, toolCallsJSON, nullable(msg.ToolCallID), msg.IsError, nullable(msg.Elided), time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("appending message: %w", err)
	}
	return nil
}

// ElideMessage records the stand-in sent instead of a tool result's full
// output: the index-th message of the session, which must answer
// toolCallID, so a stale index can't elide the wrong row.
func (s *Store) ElideMessage(sessionID string, index int, toolCallID, stub string) error {
	_, err := s.db.Exec(
		`UPDATE messages SET elided = ? WHERE tool_call_id = ? AND id = (SELECT id FROM messages WHERE session_id = ? ORDER BY id LIMIT 1 OFFSET ?)`,
		stub, toolCallID, sessionID, index,
	)
	if err != nil {
		return fmt.Errorf("eliding message: %w", err)
	}
	return nil
}

// ElideToolCalls records the tool calls of the index-th message of the
// session with their elided arguments. The row must be an assistant
// message carrying the first call's id, so a stale index can't touch the
// wrong row.
func (s *Store) ElideToolCalls(sessionID string, index int, calls []model.ToolCall) error {
	if len(calls) == 0 {
		return nil
	}
	b, err := encodeCalls(calls)
	if err != nil {
		return err
	}
	firstID, _ := json.Marshal(calls[0].ID)
	_, err = s.db.Exec(
		`UPDATE messages SET tool_calls = ? WHERE role = 'assistant' AND instr(tool_calls, ?) > 0 AND id = (SELECT id FROM messages WHERE session_id = ? ORDER BY id LIMIT 1 OFFSET ?)`,
		b, string(firstID), sessionID, index,
	)
	if err != nil {
		return fmt.Errorf("eliding tool calls: %w", err)
	}
	return nil
}

// LoadHistory returns a session's messages in append order. A message
// whose tool calls can't be decoded keeps its text, and its results get
// stand-in calls, so one damaged row doesn't make the whole session
// unusable and every message keeps its index.
func (s *Store) LoadHistory(sessionID string) ([]model.Message, error) {
	rows, err := s.db.Query(
		`SELECT role, content, tool_calls, tool_call_id, is_error, elided FROM messages WHERE session_id = ? ORDER BY id ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var history []model.Message
	calls := map[string]bool{} // ids of the calls restored so far
	for rows.Next() {
		var role, content string
		var toolCallsJSON, toolCallID, elided sql.NullString
		var isError bool
		if err := rows.Scan(&role, &content, &toolCallsJSON, &toolCallID, &isError, &elided); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}

		msg := model.Message{Role: model.Role(role), Content: content, IsError: isError, Elided: elided.String}
		if toolCallsJSON.Valid {
			if err := json.Unmarshal([]byte(toolCallsJSON.String), &msg.ToolCalls); err != nil {
				msg.ToolCalls = nil
				msg.Content = strings.TrimSpace(msg.Content + "\n[wisp: this message's tool calls could not be restored]")
			}
		}
		for _, call := range msg.ToolCalls {
			calls[call.ID] = true
		}
		if toolCallID.Valid {
			msg.ToolCallID = toolCallID.String
			if msg.Role == model.RoleTool && !calls[msg.ToolCallID] {
				// Its call was lost with its message's tool calls. Dropping
				// the result would shift every later message's index, which
				// compactions and masking store; restore a stand-in call on
				// the assistant message the result answers, behind any
				// results that follow it, so the pair stays valid. With no
				// assistant message before it to restore one on, the result
				// can't be sent, and is dropped.
				last := len(history) - 1
				for last >= 0 && history[last].Role == model.RoleTool {
					last--
				}
				if last < 0 || history[last].Role != model.RoleAssistant {
					continue
				}
				history[last].ToolCalls = append(history[last].ToolCalls, model.ToolCall{ID: msg.ToolCallID, Name: "unknown", Args: json.RawMessage("{}")})
				calls[msg.ToolCallID] = true
			}
		}
		history = append(history, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading history: %w", err)
	}
	return history, nil
}
