package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

func TestAgentRunsShowInPanelAndChat(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	args := json.RawMessage(`{"agent":"explorer","task":"find where Loop is defined"}`)
	m.Update(StreamMsg{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c1", Name: agent.ToolName, Args: args}})

	running := agent.Event{RunID: 1, CallID: "c1", Agent: "explorer", Task: "find where Loop is defined", Status: agent.Running, Started: time.Now(),
		Steps: []agent.Step{{ID: "s1", Name: "grep", Args: json.RawMessage(`{"pattern":"type Loop"}`), Status: "running"}}}
	m.Update(AgentMsg(running))
	if !m.agentsBeside() {
		t.Fatal("agents panel did not open for the first run")
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Agents 1 active", "explorer", "find where Loop", "› grep type Loop", "agent explorer: find where Loop"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}

	done := running
	done.Status, done.Took, done.Report = agent.Done, 3*time.Second, "Loop is in **core/loop.go**"
	done.Steps = []agent.Step{{ID: "s1", Name: "grep", Args: json.RawMessage(`{"pattern":"type Loop"}`), Status: "ok"}}
	m.Update(AgentMsg(done))
	m.Update(ToolResultMsg{Call: model.ToolCall{ID: "c1"}, Result: tool.Result{Content: done.Report}})
	if !strings.Contains(stripANSI(m.View()), "1 tool call") {
		t.Error("finished run summary missing from the panel")
	}
}

func TestSubAgentApprovalIsLabelled(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(PermissionRequestMsg{Agent: "explorer", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`), Reply: make(chan Answer, 1)})
	if got := stripANSI(m.View()); !strings.Contains(got, "explorer ▸ bash wants to run a command") {
		t.Fatalf("approval not labelled with the sub-agent:\n%s", got)
	}
}

func TestAgentChatView(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	args := json.RawMessage(`{"agent":"explorer","task":"find Loop"}`)
	m.Update(StreamMsg{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c1", Name: agent.ToolName, Args: args}})
	m.Update(AgentMsg{RunID: 7, CallID: "c1", Agent: "explorer", Task: "find Loop", Status: agent.Running, Started: time.Now()})

	grep := model.ToolCall{ID: "s1", Name: "grep", Args: json.RawMessage(`{"pattern":"type Loop"}`)}
	for _, e := range []model.Event{
		{Kind: model.EventReasoningDelta, Reasoning: "search for the type"},
		{Kind: model.EventToolCall, ToolCall: &grep},
	} {
		m.Update(AgentTraceMsg{RunID: 7, Event: &e})
	}
	m.Update(AgentTraceMsg{RunID: 7, Result: &tool.CallResult{ToolCall: grep, Result: tool.Result{Content: "core/loop.go:29:type Loop struct"}}})
	m.Update(AgentTraceMsg{RunID: 7, Event: &model.Event{Kind: model.EventTextDelta, Text: "Loop is in core/loop.go:29"}})

	// Ctrl+O on the agent box opens the sub-agent's own chat.
	m.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.viewing != 7 {
		t.Fatalf("viewing = %d after Ctrl+O on the agent box, want run 7", m.viewing)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"explorer ▸ find Loop", "ctrl+b back", "▌ find Loop", "Thought for 4 words", "grep type Loop", "Loop is in core/loop.go:29"} {
		if !strings.Contains(view, want) {
			t.Errorf("agent chat missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Endpoint") {
		t.Error("agent chat shows the main splash")
	}

	// Main-chat events keep going to the main chat while the agent chat is shown.
	m.Update(StreamMsg{Kind: model.EventTextDelta, Text: "main answer"})
	if strings.Contains(transcriptText(m), "main answer") {
		t.Error("main-agent text appeared in the sub-agent chat")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	if m.viewing != 0 || !strings.Contains(transcriptText(m), "main answer") {
		t.Fatalf("ctrl+b did not return to the main chat (viewing %d)", m.viewing)
	}

	// A finished run settles its chat.
	m.Update(AgentMsg{RunID: 7, CallID: "c1", Agent: "explorer", Task: "find Loop", Status: agent.Failed, Err: errors.New("model went away")})
	m.openRun(7)
	if m.viewing != 7 || !strings.Contains(transcriptText(m), "model went away") {
		t.Fatalf("viewing %d, chat:\n%s", m.viewing, transcriptText(m))
	}

	// Sending a prompt from the agent chat returns to the main chat first.
	m.input.SetValue("/help")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.viewing != 0 {
		t.Fatal("submitting from the agent chat stayed on it")
	}
}

// /agents says so when no sub-agent has run; otherwise it shows and hides
// the panel.
func TestAgentsCommandTogglesPanel(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.runCommand("/agents")
	if last := m.blocks[len(m.blocks)-1]; last.kind != blockNotice || !strings.Contains(last.text, "No sub-agent") {
		t.Fatalf("/agents with no runs: last block %+v, want the no-agent notice", last)
	}

	m.updateAgentRun(agent.Event{RunID: 1, CallID: "a1", Agent: "explorer", Task: "look", Status: agent.Done})
	if !m.agentsBeside() || !strings.Contains(m.View(), "explorer") {
		t.Fatal("the first run didn't open the panel")
	}
	m.runCommand("/agents")
	if m.agentsBeside() || strings.Contains(m.View(), "explorer") {
		t.Error("/agents didn't hide the panel")
	}
	m.runCommand("/agents")
	if !m.agentsBeside() || !strings.Contains(m.View(), "explorer") {
		t.Error("/agents didn't show the panel again")
	}
}

// Esc leaves a sub-agent's chat before anything else.
func TestEscLeavesAgentChat(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(AgentMsg{RunID: 3, Agent: "explorer", Task: "look", Status: agent.Running})
	m.openRun(3)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.viewing != 0 {
		t.Errorf("Esc left the view on run %d", m.viewing)
	}
}

// The main model writes a sub-agent's task, and a failed run's error can
// quote a model or a server: none of it may drive the terminal.
func TestAgentTextShowsNoEscapes(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	evil := "\x1b]52;c;aGk=\x07\x1b[2J"
	m.Update(AgentMsg{RunID: 1, Agent: "explorer", Task: "look" + evil, Status: agent.Running, Started: time.Now()})
	m.Update(AgentMsg{RunID: 2, Agent: "explorer", Task: "fail", Status: agent.Failed, Err: errors.New("boom" + evil)})
	m.openRun(1)
	for where, view := range map[string]string{"panel and label": m.View(), "panel": func() string { m.viewMain(); return m.View() }()} {
		if strings.Contains(view, "\x1b]52") || strings.Contains(view, "\x1b[2J") {
			t.Errorf("%s drew an escape sequence: %q", where, view)
		}
	}
}

func TestSessionSwitchResetsAgents(t *testing.T) {
	store, ids := sessionStoreWith(t, "old")
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Store = store
	m.Update(AgentMsg{RunID: 1, Agent: "explorer", Task: "look", Status: agent.Running})
	m.openRun(1)
	m.resumeSession(ids[0])
	if len(m.agentRuns) != 0 || m.agentsOpen || m.viewing != 0 || len(m.runViews) != 0 {
		t.Errorf("after /resume: %d runs, open %v, viewing %d", len(m.agentRuns), m.agentsOpen, m.viewing)
	}
}
