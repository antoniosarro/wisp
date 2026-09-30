package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

func TestContextCommandFromPopup(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	typeRunes(m, "/cont")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.input.Value() != "" || len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].kind != blockContext {
		t.Fatalf("enter didn't run /context: input %q", m.input.Value())
	}
}

func TestContextView(t *testing.T) {
	stats := core.ContextStats{
		Window: 32768, Budget: 24000, History: 9000, Fixed: 3000, Ratio: 1.1,
		Prompt: 1000, Project: 800, MCPPrompt: 50, Tools: map[string]int{"read": 600, "bash": 500, "tool_search": 40, "mcp_call": 10},
		Summary: 2000, Messages: 7000, Reserve: 4096, MaskedResults: 5, MaskedSaved: 12000, Compactions: 1,
	}
	u := contextUsage{stats, "glm"}
	wide := stripANSI(renderContextBlock(u, 120))
	for _, want := range []string{
		"12.0K / 32.8K tokens (36.6%)", "Project instructions (AGENTS.md): 800 tokens", "Tool definitions: 1.1K tokens",
		"MCP servers: 100 tokens", "Summary of earlier messages: 2.0K tokens", "Messages: 7.0K tokens",
		"Free space: 16.7K tokens", "Reserved for the reply: 4.1K tokens", "5 masked, saving ~12.0K", "1 compaction ·", "×1.10",
	} {
		if !strings.Contains(wide, want) {
			t.Errorf("context view lacks %q:\n%s", want, wide)
		}
	}
	grid := u.grid()
	if n := strings.Count(stripANSI(grid), "■") + strings.Count(stripANSI(grid), "·") + strings.Count(stripANSI(grid), "×"); n != contextCells {
		t.Errorf("grid has %d cells, want %d", n, contextCells)
	}
	// Narrow: the legend, starting with the model, comes after the grid's rows.
	narrow := strings.Split(stripANSI(renderContextBlock(u, 50)), "\n")
	stacked := false
	for i, line := range narrow {
		stacked = stacked || strings.TrimSpace(line) == "glm" && i > contextRows
	}
	if !stacked {
		t.Errorf("narrow view doesn't put the legend under the grid:\n%s", strings.Join(narrow, "\n"))
	}

	u.stats.Window = 0
	if got := stripANSI(renderContextBlock(u, 120)); !strings.Contains(got, "window unknown") || strings.Contains(got, "■ ■") {
		t.Errorf("unknown window:\n%s", got)
	}
}

func TestContextBetweenTurnsReadsTheLoop(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.ContextWindow = 16384
	m.loop.History = []model.Message{{Role: model.RoleUser, Content: strings.Repeat("word ", 500)}}
	m.showContext()
	if got := m.blocks[len(m.blocks)-1].usage.stats; got.Messages < 500 || got.Window != 16384 {
		t.Errorf("snapshot = %+v, want the loop's current numbers", got)
	}
}

// Names from the endpoint and text from the model reach the panels and
// blocks this step adds: none may drive the terminal.
func TestPanelsShowNoEscapes(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	evil := "\x1b]52;c;aGk=\x07\x1b[2J"
	check := func(where, view string) {
		t.Helper()
		if strings.Contains(view, "\x1b]52") || strings.Contains(view, "\x1b[2J") {
			t.Errorf("%s drew an escape sequence: %q", where, view)
		}
	}

	args, _ := json.Marshal(map[string]any{"todos": []map[string]string{{"content": "plan" + evil, "status": "in_progress"}}})
	m.setTodos(args)
	check("task panel", m.View())

	m.opts.Model.ID = "model" + evil
	m.Update(StatsMsg{UsageAvailable: true, Provider: "provider" + evil})
	m.toggleDebug()
	check("debug panel", m.View())
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	check("full-screen debug panel", m.View())
	m.toggleDebug()
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})

	m.input.SetValue("/context")
	m.submit()
	check("/context", m.View())

	m.handleTurnMsg(CompactMsg{Done: true, Compaction: core.Compaction{TokensBefore: 2000, TokensAfter: 500}})
	m.blocks[len(m.blocks)-1].detail = "summary" + evil
	m.toggleBlock(len(m.blocks) - 1)
	check("expanded compaction", m.View())
	if !strings.Contains(stripANSI(m.View()), "summary") {
		t.Error("the expanded compaction doesn't show the summary")
	}

	m.suggestGen = 1
	m.Update(suggestionMsg{gen: 1, text: "next" + evil})
	check("suggestion", m.View())
	if !strings.Contains(stripANSI(m.View()), "next") {
		t.Error("the suggestion isn't shown")
	}
}

// On a narrow terminal /debug takes the transcript's place, and the
// scroll keys move it.
func TestDebugFullScreenOnNarrowTerminal(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	m.appendBlock(block{kind: blockAnswer, text: "chat text"})
	m.input.SetValue("/debug")
	m.submit()
	view := stripANSI(m.View())
	if !m.debugFullscreen() || !strings.Contains(view, "Debug") || strings.Contains(view, "chat text") {
		t.Fatalf("narrow /debug:\n%s", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.debug.YOffset == 0 {
		t.Error("PgDown didn't scroll the debug panel")
	}
}

func TestSessionSwitchResetsPanels(t *testing.T) {
	store, ids := sessionStoreWith(t, "old")
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Store = store
	m.setTodos(json.RawMessage(threeTodos))
	m.Update(StatsMsg{UsageAvailable: true, PromptTokens: 10})
	m.resumeSession(ids[0])
	if len(m.todos) != 0 || m.todoOpen || m.stats.PromptTokens != 0 {
		t.Errorf("after /resume: todos %v open %v, stats %+v", m.todos, m.todoOpen, m.stats)
	}
}
