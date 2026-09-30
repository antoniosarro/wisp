package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

// Screen snapshots: each state is rendered at several terminal sizes and
// compared, colours stripped, with testdata/TestSnapshots/*.golden.
// Regenerate after an intended change with:
//
//	go test ./internal/tui -run TestSnapshots -update
//
// Every render, snapshot or not, must also satisfy checkScreen.

// snapshotState puts a model into one visual state.
type snapshotState struct {
	name  string
	build func(m *Model)
}

var snapshotStates = []snapshotState{
	{"splash", func(m *Model) {}},
	{"conversation", func(m *Model) { m.blocks = conversationBlocks() }},
	{"streaming", func(m *Model) {
		m.inTurn = true
		m.blocks = append(conversationBlocks(),
			block{kind: blockUser, text: "now run the tests"},
			block{kind: blockReasoning, reasoningText: "The tests live in ./internal", reasoningStart: time.Unix(0, 0)},
		)
		b := toolCallBlock(model.ToolCall{ID: "t2", Name: "bash", Args: json.RawMessage(`{"command":"go test ./..."}`)})
		b.toolStart = time.Unix(0, 0)
		m.blocks = append(m.blocks, b)
	}},
	{"approval", awaitApproval},
	{"deny_note", func(m *Model) {
		awaitApproval(m)
		m.startNoting()
		m.input.SetValue("use make clean instead")
	}},
	{"picker", func(m *Model) {
		m.opts.Model = model.Info{ID: "qwen3-coder"}
		m.showModels([]model.Info{
			{ID: "qwen3-coder", ContextWindow: 65536, Tools: model.Supported, Loaded: true},
			{ID: "gemma3", ContextWindow: 16384, MaxContext: 131072, Vision: model.Supported},
			{ID: "deepseek/v3", ContextWindow: 163840, Tools: model.Supported, Price: model.Pricing{Known: true, Input: 0.27, Output: 1.1}},
		}, "")
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}},
	{"debug", func(m *Model) {
		m.blocks = conversationBlocks()
		m.debugOpen = true
		m.opts.Model = model.Info{ID: "fast-agent", ContextWindow: 32768, Price: model.Pricing{Known: true, Input: 3, Output: 15}}
		m.stats = core.StepStats{Context: core.ContextStats{Window: 32768, Fixed: 2600, Budget: 26072, History: 9400, Ratio: 1.08, MaskedResults: 4}, UsageAvailable: true, PromptTokens: 12000, CompletionTokens: 800, CachedTokens: 9000,
			SessionPromptTokens: 30000, SessionCompletionTokens: 2000, SessionCachedTokens: 20000,
			SystemTokensEst: 900, HistoryTokensEst: 11000, ToolDefTokensEst: 1500, AnswerTokensEst: 700,
			Duration: 3 * time.Second, TokensPerSec: 266.7, RequestCount: 4}
		m.cost.add(m.stats, m.opts.Model)
	}},
	{"subagent", func(m *Model) {
		m.blocks = []block{{kind: blockUser, text: "find the retry logic"}, toolCallBlock(model.ToolCall{ID: "a1", Name: agent.ToolName, Args: json.RawMessage(`{"agent":"explorer","task":"find retry logic"}`)})}
		run := agent.Event{RunID: 1, CallID: "a1", Agent: "explorer", Task: "find retry logic", Status: agent.Running, Started: time.Unix(0, 0)}
		m.updateAgentRun(run)
		view := blockList{{kind: blockUser, text: "find retry logic"}, {kind: blockAnswer, text: "Retries live in `client.go`: `post` retries 429 and 5xx."}}
		m.runViews[1] = &view
		m.viewing = 1
	}},
	{"error", func(m *Model) {
		m.blocks = []block{{kind: blockUser, text: "hi"}, {kind: blockTurnError, text: "stream error: 500 Internal Server Error: upstream timed out"}}
	}},
	{"help", func(m *Model) { m.showHelp() }},
	{"approval_write", func(m *Model) {
		awaitApprovalOf(m, "write", `{"path":"docs/notes.md","content":"# Notes\n\nThe loop streams, dispatches tools, and repeats.\n"}`)
	}},
	{"approval_edit", func(m *Model) {
		awaitApprovalOf(m, "edit", `{"path":"internal/core/loop.go","old_string":"maxIter := 20\nstep()","new_string":"maxIter := 50\nstep()\nlog()"}`)
	}},
	{"denied", func(m *Model) {
		denied := toolCallBlock(model.ToolCall{ID: "t1", Name: "bash", Args: json.RawMessage(`{"command":"rm -rf build/"}`)})
		denied.toolStatus, denied.toolResult = toolDenied, "use make clean instead"
		m.blocks = []block{{kind: blockUser, text: "clean up"}, denied, {kind: blockAnswer, text: "Understood, I'll run `make clean`."}}
	}},
	{"command_popup", func(m *Model) {
		m.blocks = conversationBlocks()
		m.input.SetValue("/c")
	}},
	{"todo_panel", func(m *Model) {
		m.blocks = conversationBlocks()
		m.setTodos(json.RawMessage(`{"todos":[{"content":"read the loop","status":"completed"},{"content":"fix the vet error","status":"in_progress"},{"content":"run the tests","status":"pending"}]}`))
	}},
	{"agents_panel", func(m *Model) {
		m.blocks = []block{{kind: blockUser, text: "find the retry logic"}, toolCallBlock(model.ToolCall{ID: "a1", Name: agent.ToolName, Args: json.RawMessage(`{"agent":"explorer","task":"find retry logic"}`)})}
		m.updateAgentRun(agent.Event{RunID: 1, CallID: "a1", Agent: "explorer", Task: "find retry logic", Status: agent.Running, Started: time.Unix(0, 0)})
		m.updateAgentRun(agent.Event{RunID: 2, CallID: "a2", Agent: "reviewer", Task: "review client.go", Status: agent.Done, Took: 4 * time.Second, PromptTokens: 1200, CompletionTokens: 300})
	}},
	{"context", func(m *Model) {
		m.blocks = conversationBlocks()
		m.opts.Model = model.Info{ID: "fast-agent", ContextWindow: 32768}
		m.loop.ContextWindow = 32768
		m.loop.History = []model.Message{
			{Role: model.RoleUser, Content: "what does the loop do?"},
			{Role: model.RoleAssistant, Content: "The loop streams a response, runs the tools it asks for, and repeats."},
		}
		m.showContext()
	}},
	{"compaction", func(m *Model) {
		m.blocks = conversationBlocks()
		m.applyCompaction(core.CompactEvent{Done: true, Compaction: core.Compaction{TokensBefore: 28400, TokensAfter: 4100}})
		m.blocks = append(m.blocks, block{kind: blockUser, text: "and now?"})
	}},
}

// awaitApproval shows a risky call waiting for the user's answer.
func awaitApproval(m *Model) { awaitApprovalOf(m, "bash", `{"command":"rm -rf build/"}`) }

// awaitApprovalOf leaves the turn waiting for approval of one tool call.
func awaitApprovalOf(m *Model, name, args string) {
	m.inTurn = true
	m.blocks = []block{{kind: blockUser, text: "clean up"}, toolCallBlock(model.ToolCall{ID: "t1", Name: name, Args: json.RawMessage(args)})}
	m.pending = []PermissionRequestMsg{{Name: name, Args: json.RawMessage(args), Reply: make(chan Answer, 1)}}
}

var snapshotSizes = [][2]int{{20, 8}, {60, 24}, {80, 24}, {120, 40}}

// conversationBlocks is a finished exchange with the common block kinds.
func conversationBlocks() []block {
	read := toolCallBlock(model.ToolCall{ID: "t1", Name: "read", Args: json.RawMessage(`{"path":"internal/core/loop.go"}`)})
	read.toolStatus, read.toolResult = toolOK, "     1\tpackage core\n     2\t\n     3\timport \"context\""
	failed := toolCallBlock(model.ToolCall{ID: "t3", Name: "bash", Args: json.RawMessage(`{"command":"go vet ./..."}`)})
	failed.toolStatus, failed.toolResult = toolFailed, "loop.go:12: unreachable code\n[exit code 1]"
	return []block{
		{kind: blockUser, text: "what does the loop do?"},
		{kind: blockReasoning, reasoningText: "Let me read the loop first.", reasoningDone: true, reasoningTime: 1500 * time.Millisecond},
		read,
		failed,
		{kind: blockAnswer, text: "The loop **streams** a response, runs the tools it asks for, and repeats.\n\n```go\nfor i := 0; i < maxIter; i++ {\n\tstep()\n}\n```"},
	}
}

// snapshotModel builds a model in state st at size w×h, with a frozen
// clock so live timers render the same every time.
func snapshotModel(t testing.TB, st snapshotState, w, h int) *Model {
	t.Helper()
	orig := elapsedSince
	elapsedSince = func(time.Time) time.Duration { return 1500 * time.Millisecond }
	t.Cleanup(func() { elapsedSince = orig })

	loop := &core.Loop{Provider: &testutil.ScriptedProvider{}, SessionID: "test-session"}
	opts := testOptions
	opts.WorkDir = "/home/user/project"
	m := NewModel(t.Context(), loop, func(tea.Msg) {}, nil, opts)
	st.build(m)
	m.mascot = m.mascotState() // what the next animation tick would show
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func TestSnapshots(t *testing.T) {
	for _, st := range snapshotStates {
		for _, size := range snapshotSizes {
			t.Run(fmt.Sprintf("%s_%dx%d", st.name, size[0], size[1]), func(t *testing.T) {
				view := snapshotModel(t, st, size[0], size[1]).View()
				checkScreen(t, view, size[0], size[1])
				golden.RequireEqual(t, []byte(stripANSI(view)))
			})
		}
	}
}

// TestScreenFitsAnySize renders every state across a sweep of terminal
// sizes and checks the layout invariants.
func TestScreenFitsAnySize(t *testing.T) {
	for _, st := range snapshotStates {
		for w := 20; w <= 200; w += 13 {
			for h := 8; h <= 60; h += 9 {
				checkScreen(t, snapshotModel(t, st, w, h).View(), w, h)
			}
		}
	}
}

// FuzzScreenFits looks for terminal sizes and states that break the
// layout: go test ./internal/tui -fuzz FuzzScreenFits
func FuzzScreenFits(f *testing.F) {
	for i := range snapshotStates {
		f.Add(uint8(0), uint8(0), uint8(i))
		f.Add(uint8(60), uint8(16), uint8(i))
	}
	f.Fuzz(func(t *testing.T, w, h, state uint8) {
		width, height := 20+int(w), 8+int(h)%80
		st := snapshotStates[int(state)%len(snapshotStates)]
		checkScreen(t, snapshotModel(t, st, width, height).View(), width, height)
	})
}

// checkScreen verifies what every frame must satisfy: it fits the
// terminal's height, no line is wider than the terminal, and the
// outer frames are whole: the screen starts with a box's top-left corner
// and ends with the footer's bottom border. Cards inside the transcript
// may be cut at its edges: that is scrolling.
func checkScreen(t testing.TB, view string, w, h int) {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	if len(lines) > h {
		t.Errorf("%dx%d: %d lines, more than the terminal's %d\n%s", w, h, len(lines), h, ansi.Strip(view))
	}
	for i, l := range strings.Split(view, "\n") {
		if lw := ansi.StringWidth(l); lw > w {
			t.Errorf("%dx%d: line %d is %d cells wide: %q", w, h, i+1, lw, ansi.Strip(l))
		}
	}
	first, last := lines[0], lines[len(lines)-1]
	if !strings.HasPrefix(first, "╭") || !strings.HasPrefix(last, "╰") || !strings.HasSuffix(strings.TrimRight(last, " "), "╯") {
		t.Errorf("%dx%d: outer frame cut: first line %q, last line %q\n%s", w, h, first, last, ansi.Strip(view))
	}
}

// TestLayoutsFillTheScreen: with room for a chat box, every layout uses
// the whole height, so the input box sits at the bottom of the screen.
func TestLayoutsFillTheScreen(t *testing.T) {
	for _, st := range snapshotStates {
		for _, size := range [][2]int{{60, 24}, {120, 40}} {
			view := snapshotModel(t, st, size[0], size[1]).View()
			if n := len(strings.Split(view, "\n")); n != size[1] {
				t.Errorf("%s at %dx%d: %d lines, want %d", st.name, size[0], size[1], n, size[1])
			}
		}
	}
}
