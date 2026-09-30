package tui

import (
	"strings"

	"github.com/antoniosarro/wisp/internal/agent"
)

// AgentTraceMsg is one raw step of a sub-agent's conversation.
type AgentTraceMsg agent.Trace

// shown is the conversation on screen: a sub-agent's, or the main chat.
func (m *Model) shown() *blockList {
	if view := m.runViews[m.viewing]; view != nil {
		return view
	}
	return &m.blocks
}

// openRun shows a sub-agent's conversation in place of the main chat.
func (m *Model) openRun(runID int64) {
	if m.runViews[runID] == nil {
		return
	}
	m.switchView(runID)
}

// viewMain returns to the main chat.
func (m *Model) viewMain() { m.switchView(0) }

// switchView shows run runID's conversation, or the main chat for 0.
func (m *Model) switchView(runID int64) {
	m.viewing = runID
	m.selectedBlock = -1
	m.autoScroll = true
	m.applyLayout()
}

// trackRun keeps a run's conversation in step with its status: the task
// opens it, and a finished run settles what was left open.
func (m *Model) trackRun(prev *agent.Event, e agent.Event) {
	view := m.runViews[e.RunID]
	if view == nil {
		view = &blockList{{kind: blockUser, text: e.Task}}
		m.runViews[e.RunID] = view
	}
	finished := e.Status == agent.Done || e.Status == agent.Failed
	if finished && (prev == nil || (prev.Status != agent.Done && prev.Status != agent.Failed)) {
		view.settle(e.Err)
	}
}

// applyTrace adds a step of a sub-agent's conversation to its view.
func (m *Model) applyTrace(t agent.Trace) {
	view := m.runViews[t.RunID]
	if view == nil {
		return
	}
	switch {
	case t.Event != nil:
		view.appendEvent(*t.Event)
	case t.Result != nil:
		view.resolve(t.Result.ToolCall, t.Result.Result, t.Result.Err)
	}
	if m.viewing == t.RunID {
		m.dirty = true // rendered by the next frame, like the main chat
	}
}

// viewLabel names the sub-agent on screen, for the chat box border.
func (m *Model) viewLabel() (left, right string) {
	for _, r := range m.agentRuns {
		if r.RunID != m.viewing {
			continue
		}
		task := strings.Join(strings.Fields(sanitize(r.Task)), " ") // the main model wrote it
		return m.runIcon(r) + " " + styleSpinner.Render(r.Agent) + styleDim.Render(" ▸ "+task), styleDim.Render("ctrl+b back")
	}
	return "", ""
}
