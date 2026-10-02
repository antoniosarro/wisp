package tui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/session"
	"github.com/antoniosarro/wisp/internal/testutil"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestResponsiveLayout(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}, {120, 40}, {20, 8}, {10, 4}} {
		m, _ := newTestModel(t, &testutil.ScriptedProvider{})
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		check := func(state string) {
			t.Helper()
			view := m.View()
			if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatalf("%s at %v: rendered %dx%d", state, size, lipgloss.Width(view), lipgloss.Height(view))
			}
			if size[0] >= 20 && size[1] >= 8 && len(m.pending) == 0 && !strings.Contains(view, "> ") {
				t.Fatalf("footer lost: %q", view)
			}
		}
		check("idle")
		m.input.SetValue(strings.Repeat("界", 100) + "\nline two\nthree\nfour\nfive\nsix")
		m.applyLayout()
		check("multiline")
		m.pending = []PermissionRequestMsg{{Name: "bash", Args: json.RawMessage(`{"command":"` + strings.Repeat("x", 200) + `"}`), Reply: make(chan Answer, 1)}}
		m.applyLayout()
		check("approval")
		if size[0] >= 20 && size[1] >= 8 && !strings.Contains(stripANSI(m.View()), "allow") {
			t.Fatal("approval actions hidden")
		}
		m.pending = nil
		m.toggleDebug()
		check("debug")
	}
}

func TestSessionListingAndSwitch(t *testing.T) {
	store, err := session.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	id, err := store.CreateSession("test")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(id, model.Message{Role: model.RoleUser, Content: "saved prompt"}); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Store = store
	m.pickSession()
	if m.modal == nil || len(m.modal.items) != 1 || m.modal.items[0].value != id || m.modal.items[0].title != "saved prompt" {
		t.Fatal("session missing from the picker")
	}
	m.modal = nil
	m.inTurn = true
	m.resumeSession(id)
	if m.loop.SessionID == id {
		t.Fatal("switched while turn was active")
	}
	m.inTurn = false
	m.resumeSession("1") // by its number in the listing
	if m.loop.SessionID != id || len(m.loop.History) != 1 || m.inputHistory[0] != "saved prompt" {
		t.Fatal("session not restored")
	}
}

func TestTranscriptFitsAndUnicodeSurvives(t *testing.T) {
	for _, w := range []int{20, 40, 80} {
		for _, s := range []string{renderUserPrompt(strings.Repeat("界", 100), w), renderAnswer(strings.Repeat("word ", 50), w), renderAnswer("```go\n"+strings.Repeat("x", 120)+"\n```", w)} {
			if lipgloss.Width(s) > w {
				t.Fatalf("rendered width %d > %d", lipgloss.Width(s), w)
			}
		}
	}
	if card := renderToolResult("bash", nil, "a"+strings.Repeat("界", 500), 0, 60, true, false); !utf8.ValidString(card) || lipgloss.Width(card) > 60 {
		t.Fatal("collapsed error summary split a codepoint or overflowed")
	}
}

func TestProgressAndInputMessages(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.inTurn = true
	m.Update(StreamMsg{Kind: model.EventReasoningDelta, Reasoning: "thinking"})
	before := m.viewport.View()
	m.Update(m.spin.Tick())
	if m.viewport.View() == before {
		t.Fatal("spinner did not refresh viewport")
	}
	m.input.Cursor.BlinkSpeed = time.Nanosecond
	cmd := m.input.Cursor.BlinkCmd()
	blink := m.input.Cursor.Blink
	m.Update(cmd())
	if m.input.Cursor.Blink == blink {
		t.Fatal("cursor message was not forwarded to input")
	}
	m.Update(TurnDoneMsg{Err: context.Canceled})
	if !m.blocks[0].reasoningDone {
		t.Fatal("interrupted reasoning remains active")
	}
	if !m.input.Focused() {
		t.Fatal("input lost focus")
	}
}

func TestMultilineDraftAndRecall(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.inTurn = true
	m.Update(keyRunes("first"))
	m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m.Update(keyRunes("second"))
	if m.input.Value() != "first\nsecond" {
		t.Fatalf("draft=%q", m.input.Value())
	}
	m.Update(TurnDoneMsg{})
	if m.input.Value() != "first\nsecond" {
		t.Fatal("completion erased draft")
	}
	m.inputHistory = []string{"prior prompt"}
	m.historyIndex = 1
	m.Update(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	if m.input.Value() != "prior prompt" {
		t.Fatal("history not recalled")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	if m.input.Value() != "first\nsecond" {
		t.Fatal("history navigation lost draft")
	}
}

func TestCancelPendingApprovals(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.inTurn = true
	m.turnCancel = cancel
	reply := make(chan Answer, 1)
	m.Update(PermissionRequestMsg{Context: ctx, Name: "write", Reply: reply})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if ctx.Err() == nil || len(m.pending) != 0 || m.quitting {
		t.Fatal("Esc did not cancel turn and clear approvals")
	}
	m.Update(PermissionRequestMsg{Context: ctx, Name: "write", Reply: reply})
	if len(m.pending) != 0 {
		t.Fatal("stale approval appeared")
	}
}

func TestDebugPanelContextBudget(t *testing.T) {
	s := core.StepStats{UsageAvailable: true, PromptTokens: 100, SessionPromptTokens: 9000, Context: core.ContextStats{
		Window: 16384, Fixed: 2000, Budget: 10000, History: 6200, Ratio: 1.12,
		MaskedResults: 3, MaskedCalls: 1, Compactions: 2, Presummary: "ready",
	}}
	out := stripANSI(renderDebugPanel(s, Options{Model: model.Info{ID: "test-model"}}, costSummary{}, 0, 0, sidePanelWidth))
	for _, want := range []string{"window      16.4K", "used        8.2K (50%)", "fixed     2.0K", "history   6.2K of 10.0K budget", "62%", "mask at     6.0K", "summarize   at 8.5K", "×1.12", "masked      3 outputs", "bodies    1", "2×", "presummary  ready"} {
		if !strings.Contains(out, want) {
			t.Errorf("debug panel lacks %q:\n%s", want, out)
		}
	}
	s.Context = core.ContextStats{History: 500, Ratio: 1}
	if out := stripANSI(renderDebugPanel(s, Options{}, costSummary{}, 0, 0, sidePanelWidth)); !strings.Contains(out, "unknown") {
		t.Errorf("unknown window not shown:\n%s", out)
	}
}

func TestHistoryReplaysToolsAndExpansion(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	result := strings.Repeat("line\n", 30) + "final hidden line"
	m.replayHistory([]model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call", Name: "read", Args: json.RawMessage(`{"path":"a"}`)}}},
		{Role: model.RoleTool, ToolCallID: "call", Content: result},
	})
	if len(m.blocks) != 1 || m.blocks[0].toolResult != result {
		t.Fatal("tool history lost")
	}
	m.selectedBlock = 0
	m.expandSelected()
	if !strings.Contains(m.render(), "final hidden line") {
		t.Fatal("expanded tool output remains truncated")
	}
}

func TestPickModel(t *testing.T) {
	models := []model.Info{{ID: "a"}, {ID: "b"}}
	for sel, want := range map[string]string{"2": "b", "a": "a", "3": "", "c": ""} {
		got, ok := pickModel(models, sel)
		if ok != (want != "") || (ok && got != want) {
			t.Errorf("pickModel(%q) = %q, %v; want %q", sel, got, ok, want)
		}
	}
}

func TestReasoningSummaryShowsDuration(t *testing.T) {
	got := stripANSI(renderReasoningSummary("a b c", 1500*time.Millisecond))
	if got != "▸ Thought for 3 words · 1.5s" {
		t.Fatalf("summary = %q", got)
	}
	if got := formatDuration(65 * time.Second); got != "1m 05s" {
		t.Fatalf("formatDuration = %q", got)
	}
}

func TestBorderLabelKeepsWidth(t *testing.T) {
	box := styleChatBox.Width(40).Render("hi")
	got := withBorderLabel(box, "Copied 3 characters")
	if lipgloss.Width(got) != lipgloss.Width(box) || !strings.Contains(stripANSI(got), "Copied 3 characters") {
		t.Fatalf("labelled box = %q", stripANSI(got))
	}
}

func TestApprovalBoxLayout(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.opts.WorkDir = "/tmp/project"
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m.pending = []PermissionRequestMsg{
		{Name: "edit", Args: json.RawMessage(`{"path":"a.go","old_string":"foo()","new_string":"bar()"}`), Reply: make(chan Answer, 1)},
		{Name: "bash", Args: json.RawMessage(`{"command":"make test"}`), Reply: make(chan Answer, 1)},
	}
	m.applyLayout()
	view := stripANSI(m.View())
	for _, want := range []string{"Permission required", "1 of 2", "edit wants to edit a.go", "- foo()", "+ bar()", "in /tmp/project", "y allow", "n deny", "esc cancel turn"} {
		if !strings.Contains(view, want) {
			t.Errorf("approval view missing %q:\n%s", want, view)
		}
	}
	footer := m.footer()
	for _, l := range strings.Split(footer, "\n") {
		if lipgloss.Width(l) != 80 {
			t.Fatalf("approval box line is %d wide, want 80: %q", lipgloss.Width(l), stripANSI(l))
		}
	}
}

func TestSingleApprovalBorderIsUnbroken(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m.pending = []PermissionRequestMsg{{Name: "bash", Args: json.RawMessage(`{"command":"ls"}`), Reply: make(chan Answer, 1)}}
	m.applyLayout()
	top := stripANSI(strings.Split(m.footer(), "\n")[0])
	if !strings.HasSuffix(top, "──╮") || strings.Contains(top, "  ") {
		t.Fatalf("top border = %q", top)
	}
	if m.approval.Height > 5 {
		t.Fatalf("approval details height = %d for a one-line command", m.approval.Height)
	}
}

func TestReplayHidesHarnessReminders(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.replayHistory([]model.Message{
		{Role: model.RoleUser, Content: "do it"},
		{Role: model.RoleAssistant, Content: "done"},
		{Role: model.RoleUser, Content: core.ReminderPrefix + "finish your tasks"},
		{Role: model.RoleAssistant, Content: "really done"},
	})
	if strings.Contains(transcriptText(m), "finish your tasks") || len(m.inputHistory) != 1 {
		t.Fatalf("reminder shown or recalled: %q, history %v", transcriptText(m), m.inputHistory)
	}
}
