package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/agent"
)

// AgentMsg is a sub-agent run snapshot forwarded from the agent tool.
type AgentMsg agent.Event

// maxPanelRuns is how many runs, the latest, the Agents panel lists.
const maxPanelRuns = 6

// agentsBeside says whether the Agents panel is in the side column.
func (m *Model) agentsBeside() bool {
	return m.agentsOpen && len(m.agentRuns) > 0 && m.width >= minSplitWidth
}

// updateAgentRun stores the latest snapshot of a run, opening the panel
// for the first run of the session.
func (m *Model) updateAgentRun(e agent.Event) {
	for i := range m.agentRuns {
		if m.agentRuns[i].RunID == e.RunID {
			m.trackRun(&m.agentRuns[i], e)
			m.agentRuns[i] = e
			m.dirty = true
			return
		}
	}
	m.trackRun(nil, e)
	if len(m.agentRuns) == 0 {
		m.agentsOpen = true
	}
	m.agentRuns = append(m.agentRuns, e)
	m.applyLayout()
}

// agentRunFor returns the run started by the main agent's tool call id.
func (m *Model) agentRunFor(callID string) *agent.Event {
	for i := range m.agentRuns {
		if m.agentRuns[i].CallID == callID {
			return &m.agentRuns[i]
		}
	}
	return nil
}

// toggleAgents shows or hides the Agents panel.
func (m *Model) toggleAgents() {
	if len(m.agentRuns) == 0 {
		m.notify("No sub-agent has run yet in this session.")
		return
	}
	m.agentsOpen = !m.agentsOpen
	m.applyLayout()
}

// agentTokens is what the session's sub-agents have used.
func (m *Model) agentTokens() int {
	total := 0
	for _, r := range m.agentRuns {
		total += r.PromptTokens + r.CompletionTokens
	}
	return total
}

// panelRuns are the runs the Agents panel lists, oldest first.
func (m *Model) panelRuns() []agent.Event {
	return m.agentRuns[max(0, len(m.agentRuns)-maxPanelRuns):]
}

// runActive reports whether a run is running or queued.
func runActive(r agent.Event) bool {
	return r.Status == agent.Waiting || r.Status == agent.Running
}

// runIcon shows a run's status.
func (m *Model) runIcon(r agent.Event) string {
	switch r.Status {
	case agent.Waiting:
		return styleDim.Render("…")
	case agent.Done:
		return styleToolOK.Render("✓")
	case agent.Failed:
		return styleToolFailed.Render("✗")
	}
	return styleSpinner.Render(strings.TrimSpace(ansi.Strip(m.spin.View())))
}

// agentsPanel renders the most recent runs; height <= 0 means fit the content.
func (m *Model) agentsPanel(height int) string {
	runs := m.panelRuns()
	inner := max(10, sidePanelWidth-4)
	active := 0
	parts := make([]string, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- { // newest first
		r := runs[i]
		if runActive(r) {
			active++
		}
		parts = append(parts, m.renderRun(r, inner))
	}
	title := styleSplashHead.Render("Agents")
	if active > 0 {
		title += " " + styleDim.Render(fmt.Sprintf("%d active", active))
	}
	return renderSidePanel(title+"\n\n"+strings.Join(parts, "\n\n"), height, sidePanelWidth)
}

// renderRun shows a run's status, task, and current step or outcome.
func (m *Model) renderRun(r agent.Event, width int) string {
	var timing, detail string
	switch r.Status {
	case agent.Waiting:
		detail = "waiting for a free slot"
	case agent.Running:
		timing, detail = formatDuration(elapsedSince(r.Started)), runActivity(r)
	case agent.Done:
		timing = formatDuration(r.Took)
		detail = plural(len(r.Steps), "tool call")
		if tokens := r.PromptTokens + r.CompletionTokens; tokens > 0 {
			detail += fmt.Sprintf(" · %d tokens", tokens)
		}
	case agent.Failed:
		timing = formatDuration(r.Took)
		if r.Err != nil {
			detail = sanitize(r.Err.Error()) // can quote the model or a server
		}
	}
	head := m.runIcon(r) + " " + styleToolText.Render(r.Agent)
	if timing != "" {
		head += styleDim.Render(" · " + timing)
	}
	// The main model wrote the task.
	task := ansi.Truncate(strings.Join(strings.Fields(sanitize(r.Task)), " "), width-2, "…")
	return head + "\n  " + styleDim.Render(task) + "\n  " + ansi.Truncate(detail, width-2, "…")
}

// runActivity describes what a running sub-agent is doing now.
func runActivity(r agent.Event) string {
	if r.Activity != "" {
		return sanitize(r.Activity) + "…"
	}
	if n := len(r.Steps); n > 0 {
		s := r.Steps[n-1]
		return "› " + sanitize(s.Name) + " " + toolDetail(s.Name, sanitizeJSON(s.Args, sanitize))
	}
	return ""
}

// plural is "1 noun" or "n nouns".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
