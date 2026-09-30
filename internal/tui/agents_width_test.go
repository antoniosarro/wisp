package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

// A live agent box must fit the transcript, or every line wraps into a blank one.
func TestLiveAgentBoxFitsWidth(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m.Update(AgentMsg{RunID: 1, Agent: "explorer", Status: agent.Running})
	args := json.RawMessage(`{"agent":"explorer","task":"Find files defining or using 'timeout' in bash commands"}`)
	m.Update(StreamMsg{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c1", Name: agent.ToolName, Args: args}})
	m.Update(AgentMsg{RunID: 1, CallID: "c1", Agent: "explorer", Task: "x", Status: agent.Running, Started: time.Now(), Activity: "thinking",
		Thinking: strings.Repeat("Okay, let me try to figure out how to approach this. ", 6),
		Steps:    []agent.Step{{ID: "s", Name: "grep", Args: json.RawMessage(`{"path":"."}`), Status: "ok"}}})
	w := m.chatWidth()
	b := &m.blocks[0]
	raw := m.renderToolCallBlock(b, w)
	for i, l := range strings.Split(raw, "\n") {
		if lipgloss.Width(l) > w {
			t.Errorf("line %d is %d wide, over %d: %q", i, lipgloss.Width(l), w, stripANSI(l))
		}
	}
}
