package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

const threeTodos = `{"todos":[{"content":"plan","status":"completed"},{"content":"build it","status":"in_progress"},{"content":"test","status":"pending"}]}`

func TestTodoPanelOpensOnFirstList(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	chatOnly := m.chatWidth()

	m.Update(StreamMsg{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "t1", Name: "todo", Args: json.RawMessage(threeTodos)}})
	if !m.todoBeside() || m.chatWidth() >= chatOnly {
		t.Fatalf("todo panel not shown beside the chat (open=%v, chat width %d -> %d)", m.todoOpen, chatOnly, m.chatWidth())
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Tasks 1/3", "✓ plan", "build it", "○ test"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}

	m.toggleTodo()
	m.Update(StreamMsg{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "t2", Name: "todo", Args: json.RawMessage(threeTodos)}})
	if m.todoBeside() {
		t.Fatal("a later update reopened the panel the user closed")
	}
}

func TestSidePanelsArrangeForEveryCombination(t *testing.T) {
	baseCorners := -1 // closed boxes in the chat-only layout (chat, input, logo art)
	for combo := range 4 {
		todo, debug := combo&1 != 0, combo&2 != 0
		var want []string
		m, _ := newTestModel(t, &testutil.ScriptedProvider{})
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
		if todo {
			m.setTodos(json.RawMessage(threeTodos))
			want = append(want, "Tasks")
		}
		if debug {
			want = append(want, "Debug")
		}
		m.debugOpen = debug
		m.applyLayout()

		name := fmt.Sprintf("todo=%v debug=%v", todo, debug)
		view := m.View()
		if lipgloss.Width(view) != 120 || lipgloss.Height(view) != 24 {
			t.Errorf("%s: view is %dx%d, want 120x24", name, lipgloss.Width(view), lipgloss.Height(view))
		}
		if (m.sideWidth() > 0) != (len(want) > 0) {
			t.Errorf("%s: sideWidth = %d", name, m.sideWidth())
		}
		plain := stripANSI(view)
		corners := strings.Count(plain, "╰")
		if baseCorners < 0 {
			baseCorners = corners
		}
		if corners-baseCorners != len(want) {
			t.Errorf("%s: %d closed side panels, want %d (a frame was cut off)", name, corners-baseCorners, len(want))
		}
		last := -1
		for _, w := range want { // present, and stacked in order
			i := strings.Index(plain, w)
			if i < 0 || i < last {
				t.Errorf("%s: %q missing or out of order", name, w)
			}
			last = i
		}
	}
}

func TestNarrowTerminalKeepsTasksInChat(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.setTodos(json.RawMessage(threeTodos))
	if m.todoBeside() || m.sideWidth() != 0 {
		t.Fatal("side panel shown on a narrow terminal")
	}
	if lipgloss.Width(m.View()) > 60 {
		t.Fatal("narrow view overflows")
	}
}

func TestTodoPanelFollowsThePlan(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	list := func(statuses ...string) json.RawMessage {
		var items []string
		for i, s := range statuses {
			items = append(items, fmt.Sprintf(`{"content":"task %d","status":%q}`, i, s))
		}
		return json.RawMessage(`{"todos":[` + strings.Join(items, ",") + `]}`)
	}

	m.setTodos(list("in_progress", "pending"))
	if !m.todoBeside() {
		t.Fatal("a new plan didn't open the panel")
	}
	m.setTodos(list("completed", "in_progress"))
	if !m.todoBeside() {
		t.Fatal("the panel closed before the plan was done")
	}
	m.setTodos(list("completed", "completed"))
	if m.todoBeside() {
		t.Fatal("the panel stayed open with every task done")
	}
	m.toggleTodo()
	if !m.todoBeside() {
		t.Fatal("/todo didn't reopen a finished list")
	}
	m.toggleTodo()
	m.setTodos(list("pending"))
	if !m.todoBeside() {
		t.Fatal("a new plan after a finished one didn't open the panel")
	}

	// A resumed session whose last plan was finished starts with it closed.
	m2, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m2.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m2.replayHistory([]model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "a", Name: "todo", Args: list("in_progress")}}},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "b", Name: "todo", Args: list("completed")}}},
	})
	if m2.todoBeside() {
		t.Error("replayed finished plan left the panel open")
	}
}
