package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/testutil"
)

func mouse(action tea.MouseAction, y int) tea.MouseMsg {
	return tea.MouseMsg{X: 10, Y: y, Action: action, Button: tea.MouseButtonLeft}
}

func TestClickTogglesToolBlock(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m.blocks = []block{{kind: blockToolCall, toolName: "read", toolArgs: json.RawMessage(`{"path":"a"}`), toolStatus: toolOK, toolResult: strings.Repeat("line\n", 20) + "last"}}
	m.autoScroll = false
	m.syncViewport()

	y := m.blockLines[0].start - m.viewport.YOffset + chatOriginY
	m.Update(mouse(tea.MouseActionPress, y))
	m.Update(mouse(tea.MouseActionRelease, y))
	if !m.blocks[0].expanded || m.selectedBlock != 0 {
		t.Fatalf("expanded=%v selected=%d after click, want expanded and selected", m.blocks[0].expanded, m.selectedBlock)
	}
	if !strings.Contains(transcriptText(m), "last") {
		t.Fatal("expanded block hides the end of its result")
	}
}

func TestDragSelectsAndStripsCardBorders(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.content = "  ╭──────╮\n  │ func │\n  │   x  │\n  ╰──────╯"
	m.sel = textSelection{anchor: textPos{0, 0}, head: textPos{3, 20}, dragged: true}
	if got, want := m.selectedText(), "func\n  x"; got != want {
		t.Fatalf("selectedText() = %q, want %q", got, want)
	}
	m.content = "  ● hello world\n    second line"
	m.sel = textSelection{anchor: textPos{0, 4}, head: textPos{1, 9}, dragged: true}
	if got, want := m.selectedText(), "hello world\nsecond"; got != want {
		t.Fatalf("selectedText() = %q, want %q", got, want)
	}
}

func TestClickTogglesEarlierBoxInPlace(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	long := strings.Repeat("r\n", 12)
	m.blocks = []block{
		{kind: blockReasoning, reasoningText: "first thought", reasoningDone: true},
		{kind: blockToolCall, toolName: "glob", toolArgs: json.RawMessage(`{"pattern":"x"}`), toolStatus: toolOK, toolResult: long},
		{kind: blockReasoning, reasoningText: "second thought", reasoningDone: true},
		{kind: blockToolCall, toolName: "read", toolArgs: json.RawMessage(`{"path":"y"}`), toolStatus: toolOK, toolResult: long},
		{kind: blockAnswer, text: "done"},
	}
	m.syncViewport()

	// Following the bottom, click the first box still in view: it must
	// expand in place rather than scroll away.
	m.autoScroll = true
	m.syncViewport()
	offset := m.viewport.YOffset
	target := -1
	for i, r := range m.blockLines {
		if r.start >= offset && m.blocks[i].collapsible() {
			target = i
			break
		}
	}
	if target < 0 || target == len(m.blocks)-1 {
		t.Fatalf("setup: no earlier box in view (offset %d, ranges %v)", offset, m.blockLines)
	}
	y := m.blockLines[target].start - offset + chatOriginY
	m.Update(mouse(tea.MouseActionPress, y))
	m.Update(mouse(tea.MouseActionRelease, y))
	if !m.blocks[target].expanded {
		t.Fatalf("block %d not expanded by click", target)
	}
	if m.viewport.YOffset != offset || m.autoScroll {
		t.Fatalf("view jumped from %d to %d (autoScroll=%v)", offset, m.viewport.YOffset, m.autoScroll)
	}
	m.viewport.GotoTop()
	was := m.blocks[0].expanded
	y = m.blockLines[0].start - m.viewport.YOffset + chatOriginY
	m.Update(mouse(tea.MouseActionPress, y))
	m.Update(mouse(tea.MouseActionRelease, y))
	if m.blocks[0].expanded == was || strings.Contains(transcriptText(m), "first thought") != !was {
		t.Fatalf("first reasoning block didn't toggle (was expanded=%v)", was)
	}
}

// blockLines must point at the lines the blocks actually occupy on screen.
func TestBlockLinesMatchRenderedContent(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.blocks = []block{
		{kind: blockUser, text: "question"},
		{kind: blockReasoning, reasoningText: "a b", reasoningDone: true},
		{kind: blockToolCall, toolName: "glob", toolArgs: json.RawMessage(`{"pattern":"x"}`), toolStatus: toolOK, toolResult: "one\ntwo"},
		{kind: blockReasoning, reasoningText: "c d", reasoningDone: true},
		{kind: blockAnswer, text: "done"},
	}
	for _, height := range []int{20, 60} { // scrolled, and padded toward the input
		m.Update(tea.WindowSizeMsg{Width: 100, Height: height})
		lines := strings.Split(m.content, "\n")
		for i, r := range m.blockLines {
			got := strings.Join(lines[r.start:r.end], "\n")
			if got != m.blocks[i].cached {
				t.Fatalf("height %d, block %d at %v:\n got %q\nwant %q", height, i, r, stripANSI(got), stripANSI(m.blocks[i].cached))
			}
		}
	}
}

func TestHoverMarksBlockUnderPointer(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.blocks = []block{
		{kind: blockUser, text: "question"},
		{kind: blockReasoning, reasoningText: "a b", reasoningDone: true},
		{kind: blockAnswer, text: "done"},
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	y := m.blockLines[1].start - m.viewport.YOffset + chatOriginY
	m.Update(tea.MouseMsg{X: 10, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if m.hoverBlock != 1 {
		t.Fatalf("hoverBlock = %d, want 1", m.hoverBlock)
	}
	if line := strings.Split(m.content, "\n")[m.blockLines[1].start]; !strings.HasPrefix(stripANSI(line), "▌") {
		t.Fatalf("hovered line %q has no gutter bar", stripANSI(line))
	}
	m.Update(tea.MouseMsg{X: 10, Y: 100, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if m.hoverBlock != -1 {
		t.Fatalf("hoverBlock = %d after leaving the transcript, want -1", m.hoverBlock)
	}
}

func TestHoverAndClickIgnorePlainText(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.blocks = []block{
		{kind: blockAnswer, text: "plain answer"},
		{kind: blockToolCall, toolName: "read", toolStatus: toolOK, toolResult: "x"},
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	y := m.blockLines[0].start - m.viewport.YOffset + chatOriginY
	m.Update(tea.MouseMsg{X: 10, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if m.hoverBlock != -1 {
		t.Fatalf("hoverBlock = %d over plain text, want -1", m.hoverBlock)
	}
	m.Update(mouse(tea.MouseActionPress, y))
	m.Update(mouse(tea.MouseActionRelease, y))
	if m.selectedBlock != -1 {
		t.Fatalf("selectedBlock = %d after clicking plain text, want -1", m.selectedBlock)
	}
}

func TestToolMeta(t *testing.T) {
	for _, c := range []struct {
		text string
		took time.Duration
		want string
	}{
		{"", 0, "no output"},
		{"a", 0, "1 line"},
		{"a\nb\nc", 250 * time.Millisecond, "3 lines · 250ms"},
		{"a\nb\nc\n", 0, "3 lines"},
		{"a\n", 0, "1 line"},
		{"a", 1500 * time.Millisecond, "1 line · 1.5s"},
	} {
		if got := toolMeta(c.text, c.took); got != c.want {
			t.Errorf("toolMeta(%q, %v) = %q, want %q", c.text, c.took, got, c.want)
		}
	}
}

func TestOpeningLiveLastBlockKeepsFollowing(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.inTurn = true
	m.blocks = []block{
		{kind: blockUser, text: "question"},
		{kind: blockReasoning, reasoningText: strings.Repeat("thinking hard\n", 5)},
	}
	m.syncViewport()
	y := m.blockLines[1].start - m.viewport.YOffset + chatOriginY
	m.Update(mouse(tea.MouseActionPress, y))
	m.Update(mouse(tea.MouseActionRelease, y))
	if !m.blocks[1].expanded || !m.autoScroll || !m.viewport.AtBottom() {
		t.Fatalf("expanded=%v autoScroll=%v atBottom=%v, want expanded and following", m.blocks[1].expanded, m.autoScroll, m.viewport.AtBottom())
	}
	m.Update(StreamMsg{Kind: model.EventReasoningDelta, Reasoning: strings.Repeat("more\n", 30) + "latest thought"})
	m.Update(frameMsg{})
	if !m.viewport.AtBottom() || !strings.Contains(stripANSI(m.viewport.View()), "latest thought") {
		t.Fatal("view stopped following the streaming reasoning")
	}
}

// A real drag: press, move past the top edge (the view scrolls up with it),
// release. The selection is highlighted without changing the text, the
// release copies it, and the next key clears it.
func TestDragScrollsSelectsAndCopies(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	for i := range 30 {
		m.blocks = append(m.blocks, block{kind: blockAnswer, text: fmt.Sprintf("answer number %d", i)})
	}
	m.syncViewport()
	bottom := m.viewport.YOffset
	if bottom == 0 {
		t.Fatal("transcript fits the viewport; the drag can't scroll")
	}

	m.Update(mouse(tea.MouseActionPress, chatOriginY+3))
	m.Update(mouse(tea.MouseActionMotion, chatOriginY-1)) // above the viewport
	if m.viewport.YOffset != bottom-1 || m.autoScroll {
		t.Errorf("YOffset %d (was %d), autoScroll %v: dragging past the top should scroll up and stop following", m.viewport.YOffset, bottom, m.autoScroll)
	}
	m.Update(mouse(tea.MouseActionMotion, chatOriginY+m.viewport.Height)) // below it
	if m.viewport.YOffset != bottom {
		t.Errorf("YOffset %d, want %d: dragging past the bottom should scroll back down", m.viewport.YOffset, bottom)
	}
	m.Update(mouse(tea.MouseActionMotion, chatOriginY-1))
	if !m.sel.visible() {
		t.Fatal("drag didn't start a visible selection")
	}
	if got, want := ansi.Strip(m.highlightSelection(m.content)), ansi.Strip(m.content); got != want {
		t.Error("highlighting changed the text")
	}

	_, cmd := m.Update(mouse(tea.MouseActionRelease, chatOriginY-1))
	if cmd == nil || m.selectedText() == "" {
		t.Fatalf("release copied nothing (selected %q)", m.selectedText())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.sel != (textSelection{}) {
		t.Errorf("selection %+v survives a key press", m.sel)
	}
}

// ctrl+y copies a block's own text: what was said or run, not its frame.
func TestCopyTextOfEachBlockKind(t *testing.T) {
	for _, c := range []struct {
		b    block
		want string
	}{
		{block{kind: blockReasoning, reasoningText: "thinking it over"}, "thinking it over"},
		{block{kind: blockToolCall, toolName: "bash", toolArgs: json.RawMessage(`{"command":"ls"}`), toolResult: "a.go"}, "bash {\"command\":\"ls\"}\na.go"},
		{block{kind: blockCompaction, text: "Compacted", detail: "the summary"}, "Compacted\n\nthe summary"},
		{block{kind: blockAnswer, text: "the answer"}, "the answer"},
	} {
		if got := c.b.copyText(); got != c.want {
			t.Errorf("kind %d: copyText() = %q, want %q", c.b.kind, got, c.want)
		}
	}
}

// Ctrl+Y puts a block's text on the clipboard, which ends up pasted into
// terminals: an escape sequence in it could close a bracketed paste and
// run the rest. It is cleaned like what the screen shows.
func TestCopyTextIsCleaned(t *testing.T) {
	inject := "harmless\x1b[201~rm -rf ~\n\x1b]52;c;aGk=\x07"
	for _, b := range []block{
		{kind: blockAnswer, text: inject},
		{kind: blockReasoning, reasoningText: inject},
		{kind: blockToolCall, toolName: "bash" + inject, toolArgs: json.RawMessage(`{"command":"ls"}`), toolResult: inject},
		{kind: blockCompaction, text: "Compacted", detail: inject},
	} {
		if got := b.copyText(); strings.ContainsAny(got, "\x1b\x07") {
			t.Errorf("kind %d: copyText() = %q, want no control characters", b.kind, got)
		}
	}
	if got := (&block{kind: blockAnswer, text: "line one\n\tline two"}).copyText(); got != "line one\n\tline two" {
		t.Errorf("copyText() = %q, want newlines and tabs kept", got)
	}
}

// The approval box's key hints are buttons: a click answers as the key
// would, once the prompt has been up for approvalGuard.
func TestClickApprovalHints(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	reply := request(m, "ls")

	lines := strings.Split(m.footer(), "\n")
	row := -1
	for i, l := range lines {
		if strings.Contains(stripANSI(l), "y allow") {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("no key hints in the footer:\n%s", stripANSI(m.footer()))
	}
	y := m.height - len(lines) + row
	x := strings.Index(stripANSI(lines[row]), "y allow") + 1
	click := tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}

	m.Update(click) // the prompt just appeared
	select {
	case a := <-reply:
		t.Fatalf("an early click answered %v", a.Decision)
	default:
	}
	settled(m)
	m.Update(click)
	if a := <-reply; a.Decision != permission.Allow {
		t.Errorf("click on \"y allow\" answered %v", a.Decision)
	}

	// The details above the hints are no button, whatever they say.
	reply = request(m, "echo y allow")
	settled(m)
	details := -1
	for i, l := range strings.Split(m.footer(), "\n") {
		if strings.Contains(stripANSI(l), "$ echo y allow") {
			details = i
		}
	}
	lines = strings.Split(m.footer(), "\n")
	m.Update(tea.MouseMsg{X: strings.Index(stripANSI(lines[details]), "y allow") + 1, Y: m.height - len(lines) + details, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	select {
	case a := <-reply:
		t.Fatalf("a click on the details answered %v", a.Decision)
	default:
	}
}

// A run in the Agents panel is a button too.
func TestClickAgentPanelOpensRun(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(AgentMsg{RunID: 4, Agent: "explorer", Task: "look", Status: agent.Done})
	m.Update(AgentMsg{RunID: 5, Agent: "tester", Task: "test", Status: agent.Done})
	top, ok := m.sidePanelTop("agents")
	if !ok {
		t.Fatal("no agents panel")
	}
	// Newest first: the second entry is the older run.
	m.Update(tea.MouseMsg{X: m.width - 10, Y: top + 3 + 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.viewing != 4 {
		t.Errorf("viewing %d after clicking the second entry, want run 4", m.viewing)
	}
}
