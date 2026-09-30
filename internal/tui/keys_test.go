package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

func TestArrowKeysRecallHistory(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.recordHistory("first")
	m.recordHistory("/help")
	m.recordHistory("/help") // repeats are stored once
	m.input.SetValue("draft")

	up, down := tea.KeyMsg{Type: tea.KeyUp}, tea.KeyMsg{Type: tea.KeyDown}
	for _, step := range []struct {
		key  tea.KeyMsg
		want string
	}{{up, "/help"}, {up, "first"}, {up, "first"}, {down, "/help"}, {down, "draft"}} {
		m.Update(step.key)
		if got := m.input.Value(); got != step.want {
			t.Fatalf("after %v: input = %q, want %q", step.key, got, step.want)
		}
	}

	// Inside a multi-line draft, Up moves the cursor before recalling.
	m.input.SetValue("line one\nline two")
	m.Update(up)
	if m.input.Value() != "line one\nline two" || m.input.Line() != 0 {
		t.Fatalf("Up on the last line recalled history instead of moving: %q line %d", m.input.Value(), m.input.Line())
	}
	m.Update(up)
	if m.input.Value() != "/help" {
		t.Fatalf("Up on the first line: input = %q, want history", m.input.Value())
	}
}

func TestReplayRecordsHistory(t *testing.T) {
	loop := &core.Loop{Provider: &testutil.ScriptedProvider{}, Tools: tool.NewRegistry(), History: []model.Message{
		{Role: model.RoleUser, Content: "fix it"},
		{Role: model.RoleUser, Content: core.ReminderPrefix + "update your todos"},
		{Role: model.RoleUser, Content: "fix it"},
		{Role: model.RoleUser, Content: "test it"},
	}}
	m := NewModel(context.Background(), loop, func(tea.Msg) {}, nil, Options{})

	if got := strings.Join(m.inputHistory, "|"); got != "fix it|test it" || m.historyIndex != 2 {
		t.Errorf("history = %q at %d, want the user's prompts once each, at the draft", got, m.historyIndex)
	}
}

func TestSubmitRecordsHistory(t *testing.T) {
	m, ch := newTestModel(t, &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventDone}}}})
	typeText(m, "hello")
	pump(t, m, ch)

	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.input.Value() != "hello" {
		t.Errorf("Up after sending: input = %q, want the sent prompt", m.input.Value())
	}
}

func TestIdleCtrlC(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	ctrlC := tea.KeyMsg{Type: tea.KeyCtrlC}

	m.input.SetValue("draft")
	if _, cmd := m.Update(ctrlC); cmd != nil || m.input.Value() != "" || !strings.Contains(m.notice, "Input cleared") {
		t.Fatalf("Ctrl+C on a draft: input = %q, notice = %q; want it cleared, not quit", m.input.Value(), m.notice)
	}
	if _, cmd := m.Update(ctrlC); cmd == nil {
		t.Fatal("a second Ctrl+C right after did not quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("a second Ctrl+C right after did not quit")
	}

	m, _ = newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(ctrlC)
	m.quitArmed = time.Now().Add(-quitWindow) // the first press was a while ago
	if _, cmd := m.Update(ctrlC); cmd != nil || !m.quitArmed.After(time.Now().Add(-time.Second)) {
		t.Error("a Ctrl+C long after the first quit, want it to arm again")
	}
}

func TestCtrlCDuringTurn(t *testing.T) {
	m, ch := newTestModel(t, blockingProvider{})
	typeText(m, "hi")

	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd != nil || !strings.Contains(m.notice, "Cancelling") {
		t.Fatalf("first Ctrl+C in a turn: notice = %q, want the turn cancelled, not quit", m.notice)
	}
	// The second quits even before the cancelled turn has ended.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("second Ctrl+C returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("second Ctrl+C did not quit")
	}
	pump(t, m, ch)
}

func TestEscDeselectsThenHints(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.appendBlock(block{kind: blockAnswer, text: "hi"})
	m.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	if m.selectedBlock != 0 {
		t.Fatalf("Alt+Left selected %d, want the last block", m.selectedBlock)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.selectedBlock != -1 {
		t.Errorf("Esc left block %d selected", m.selectedBlock)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !strings.Contains(m.notice, "Ctrl+C twice") {
		t.Errorf("idle Esc: notice = %q, want the exit hint", m.notice)
	}
}

func TestSelectAndExpandBlock(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.appendBlock(toolCallBlock(model.ToolCall{ID: "1", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)}))
	m.blocks.resolve(model.ToolCall{ID: "1"}, tool.Result{Content: "main.go\ngo.mod"}, nil)
	m.appendBlock(block{kind: blockAnswer, text: "Two files."})
	m.syncViewport()

	// With nothing selected, Ctrl+O selects the last block, which has
	// nothing to expand.
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.selectedBlock != 1 || strings.Contains(stripANSI(m.View()), "main.go") {
		t.Fatalf("Ctrl+O: selected %d; want the answer selected and nothing expanded", m.selectedBlock)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	view := stripANSI(m.View())
	if m.selectedBlock != 0 || !strings.Contains(view, "$ ls") || !strings.Contains(view, "main.go") {
		t.Errorf("Alt+Left, Ctrl+O: selected %d, view:\n%s", m.selectedBlock, view)
	}
	if !strings.Contains(m.View(), styleSelectedBar.Render("▌")) {
		t.Error("the selected block has no gutter mark")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if strings.Contains(stripANSI(m.View()), "main.go") {
		t.Error("a second Ctrl+O did not collapse the output")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	m.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	if m.selectedBlock != 1 {
		t.Errorf("Alt+Right past the end selected %d, want the last block", m.selectedBlock)
	}
}

// Selecting a block scrolls to where it starts, however far up.
func TestSelectScrollsToBlock(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	for i := range 20 {
		m.appendBlock(block{kind: blockNotice, text: fmt.Sprintf("note %d", i)})
	}
	m.syncViewport()
	for range 19 {
		m.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	}
	if m.selectedBlock != 1 || !strings.HasPrefix(strings.TrimSpace(stripANSI(m.viewport.View())), "▌") {
		t.Errorf("selected %d; viewport starts:\n%s", m.selectedBlock, stripANSI(m.viewport.View()))
	}
	if m.autoScroll {
		t.Error("selecting up kept following the output")
	}
}

func TestScrollStopsAndResumesFollowing(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	for i := range 40 {
		m.appendBlock(block{kind: blockNotice, text: fmt.Sprintf("note %d", i)})
	}
	m.syncViewport()

	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.autoScroll || !strings.Contains(stripANSI(m.View()), "ctrl+end for latest") {
		t.Fatal("PgUp kept following the output, or gave no way back")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlEnd})
	if !m.autoScroll || !m.viewport.AtBottom() {
		t.Error("Ctrl+End did not return to the latest output")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !m.autoScroll {
		t.Error("Ctrl+U then Ctrl+D back to the bottom did not resume following")
	}
}
