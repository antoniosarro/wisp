package tui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/testutil"
)

func TestBlockSelectionBeforeFirstResize(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.blocks = append(m.blocks, block{kind: blockUser, text: "a"}, block{kind: blockAnswer, text: "b"})
	m.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	if m.selectedBlock < 0 {
		t.Error("selection lost")
	}
}

func TestSubAgentTraceWaitsForFrame(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.runViews[1] = &blockList{}
	m.viewing = 1
	before := m.content
	m.Update(AgentTraceMsg(agent.Trace{RunID: 1, Event: &model.Event{Kind: model.EventTextDelta, Text: "report text"}}))
	if m.content != before || !m.dirty {
		t.Fatal("a sub-agent token re-rendered the transcript at once")
	}
	m.Update(frameMsg{})
	if !strings.Contains(stripANSI(m.content), "report text") {
		t.Fatal("the frame did not render the sub-agent's text")
	}
}

func TestEnterDuringTurnExplainsAndNoticesDontYank(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.inTurn = true
	m.input.SetValue("next question")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.input.Value() != "next question" || !strings.Contains(m.notice, "Still working") {
		t.Errorf("draft %q, notice %q", m.input.Value(), m.notice)
	}
	m.autoScroll = false
	m.notify("background news")
	if m.autoScroll {
		t.Error("a notice pulled the view back to the bottom")
	}
}

func TestApprovalHintsAreClickable(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.inTurn = true
	reply := make(chan Answer, 1)
	m.Update(PermissionRequestMsg{Name: "bash", Args: json.RawMessage(`{"command":"echo 'y allow'"}`), Reply: reply})
	m.pendingSince = m.pendingSince.Add(-approvalGuard)

	lines := strings.Split(m.footer(), "\n")
	top := m.height - len(lines)
	click := func(label string, row int) {
		x := ansi.StringWidth(ansi.Strip(lines[row])[:strings.Index(ansi.Strip(lines[row]), label)])
		m.Update(tea.MouseMsg{X: x + 1, Y: top + row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	}
	for row, l := range lines { // the command text also says "y allow": not a button
		if strings.Contains(ansi.Strip(l), "echo 'y allow'") {
			click("y allow", row)
		}
	}
	select {
	case a := <-reply:
		t.Fatalf("details text acted as a button: %+v", a)
	default:
	}
	for row := len(lines) - 3; row < len(lines)-1; row++ {
		if strings.Contains(ansi.Strip(lines[row]), "n deny") {
			click("n deny", row)
		}
	}
	select {
	case a := <-reply:
		if a.Decision != permission.Deny {
			t.Errorf("clicked deny, got %+v", a)
		}
	default:
		t.Fatal("clicking the deny hint did nothing")
	}
}

func TestCopyFallsBackToOSC52OverSSH(t *testing.T) {
	var out bytes.Buffer
	orig := terminalOut
	terminalOut = &out
	defer func() { terminalOut = orig }()
	t.Setenv("SSH_TTY", "/dev/pts/1")
	t.Setenv("TMUX", "")
	msg := copyCmd("hi", "Copied")().(clipboardResultMsg)
	if msg.err != nil || !strings.Contains(msg.notice, "OSC 52") || !strings.HasPrefix(out.String(), "\x1b]52;c;aGk=") {
		t.Fatalf("msg = %+v, written %q", msg, out.String())
	}
}

func TestModelPickerArrows(t *testing.T) {
	p := &catalogProvider{models: []model.Info{{ID: "a"}, {ID: "b"}, {ID: "c"}}, current: "a"}
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Provider = p
	m.opts.Model = model.Info{ID: "a"}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.showModels(p.models, "")
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := stripANSI(m.View()); !strings.Contains(got, "› c") || !strings.Contains(got, "a  ") || !strings.Contains(got, "● current") {
		t.Fatalf("picker:\n%s", got)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.current != "c" || m.modal != nil || cmd == nil {
		t.Fatalf("current %q, picker open %v", p.current, m.modal != nil)
	}
}

func TestPermissionAnswerFindsItsOwnCall(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	args := json.RawMessage(`{"command":"make"}`)
	m.blocks = blockList{toolCallBlock(model.ToolCall{ID: "first", Name: "bash", Args: args}), toolCallBlock(model.ToolCall{ID: "second", Name: "bash", Args: args})}
	m.answerPermission(PermissionRequestMsg{CallID: "first", Name: "bash", Args: args}, false)
	if m.blocks[0].toolStatus != toolDenied || m.blocks[1].toolStatus != toolRunning {
		t.Fatalf("statuses = %v, %v; want the first call denied", m.blocks[0].toolStatus, m.blocks[1].toolStatus)
	}
}

func TestStreamingAnswerIsNotCached(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.inTurn = true
	before := len(partCache)
	for _, d := range []string{"partial ", "answer ", "text"} {
		m.Update(StreamMsg{Kind: model.EventTextDelta, Text: d})
		m.Update(frameMsg{})
	}
	if len(partCache) != before {
		t.Errorf("partCache grew by %d entries while the answer streamed", len(partCache)-before)
	}
}
