package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

// The mascot should read the agent's activity: approvals first, then
// sub-agents, tools, streaming, and finally the last turn's outcome.
func TestMascotStateFollowsActivity(t *testing.T) {
	newModel := func(t *testing.T) *Model {
		t.Helper()
		m, _ := newTestModel(t, &testutil.ScriptedProvider{})
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		return m
	}

	t.Run("idle", func(t *testing.T) {
		if got := newModel(t).mascotState(); got != mascotIdle {
			t.Fatalf("state = %v, want idle", got)
		}
	})

	t.Run("thinking", func(t *testing.T) {
		m := newModel(t)
		m.inTurn = true
		m.appendStream(StreamMsg{Kind: model.EventReasoningDelta, Reasoning: "hmm"})
		if got := m.mascotState(); got != mascotThinking {
			t.Fatalf("state = %v, want thinking", got)
		}
	})

	t.Run("writing", func(t *testing.T) {
		m := newModel(t)
		m.inTurn = true
		m.appendStream(StreamMsg{Kind: model.EventTextDelta, Text: "hi"})
		if got := m.mascotState(); got != mascotWriting {
			t.Fatalf("state = %v, want writing", got)
		}
	})

	t.Run("tool", func(t *testing.T) {
		m := newModel(t)
		m.inTurn = true
		m.blocks = []block{{kind: blockToolCall, toolName: "read", toolStatus: toolRunning}}
		if got := m.mascotState(); got != mascotTool {
			t.Fatalf("state = %v, want tool", got)
		}
	})

	t.Run("agent", func(t *testing.T) {
		m := newModel(t)
		m.inTurn = true
		m.blocks = []block{{kind: blockToolCall, toolName: "read", toolStatus: toolRunning}}
		m.agentRuns = []agent.Event{{Status: agent.Running}}
		if got := m.mascotState(); got != mascotAgent {
			t.Fatalf("state = %v, want agent (outranks tool)", got)
		}
	})

	t.Run("waiting outranks work", func(t *testing.T) {
		m := newModel(t)
		m.inTurn = true
		m.pending = []PermissionRequestMsg{{Name: "write"}}
		m.agentRuns = []agent.Event{{Status: agent.Running}}
		if got := m.mascotState(); got != mascotWaiting {
			t.Fatalf("state = %v, want waiting", got)
		}
	})

	t.Run("turn end idles, then sleeps", func(t *testing.T) {
		m := newModel(t)
		m.mascotTick = 50
		m.inTurn = true
		m.finishTurn(nil)
		if got := m.mascotState(); got != mascotIdle {
			t.Fatalf("state = %v, want idle right after the turn", got)
		}
		m.mascotTick = 50 + mascotSleepTicks
		if got := m.mascotState(); got != mascotSleeping {
			t.Fatalf("state = %v, want sleeping after a quiet minute", got)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
		if got := m.mascotState(); got != mascotIdle {
			t.Fatalf("state = %v, want idle after a key press", got)
		}
		m.mascotTick += mascotSleepTicks
		if got := m.mascotState(); got != mascotSleeping {
			t.Fatalf("state = %v, want sleeping again", got)
		}
	})

	t.Run("failed turn idles too", func(t *testing.T) {
		m := newModel(t)
		m.inTurn = true
		m.finishTurn(errors.New("boom"))
		if got := m.mascotState(); got != mascotIdle {
			t.Fatalf("state = %v, want idle", got)
		}
	})
}

// A state shows only once it has been wanted for mascotSettleTicks ticks in
// a row, so a tool call finishing between ticks never flashes, and a shown
// state's animation starts on its first frame.
func TestMascotSettlesBeforeShowing(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.inTurn = true
	m.appendStream(StreamMsg{Kind: model.EventTextDelta, Text: "hi"})
	tick := func(n int) {
		for range n {
			m.mascotTick++
			m.stepMascot()
		}
	}
	tick(mascotSettleTicks)
	if m.mascot != mascotWriting || m.mascotFrameTick() != 0 {
		t.Fatalf("shown %v at frame tick %d, want writing from frame 0", m.mascot, m.mascotFrameTick())
	}
	m.blocks = append(m.blocks, block{kind: blockToolCall, toolName: "read", toolStatus: toolRunning})
	tick(mascotSettleTicks - 1)
	m.blocks[len(m.blocks)-1].toolStatus = toolOK
	tick(1)
	if m.mascot != mascotWriting {
		t.Fatalf("a brief tool call flashed %v", m.mascot)
	}
	m.blocks[len(m.blocks)-1].toolStatus = toolRunning
	tick(mascotSettleTicks)
	if m.mascot != mascotTool {
		t.Fatalf("shown %v, want tool once it lasted", m.mascot)
	}
}

// Words in a sent message play their easter egg for a while; it outranks
// work but not a pending approval.
func TestMascotEggs(t *testing.T) {
	for msg, want := range map[string]mascotState{
		"Thanks!":                  mascotLoved,
		"ok thank you, that works": mascotLoved,
		"boo":                      mascotSpooky,
		"time for a coffee break":  mascotSipping,
		"good night wisp":          mascotSleeping,
		"hi":                       mascotWaving,
		"this is fine":             mascotIdle, // "hi" only as a word
		"tea-driven development":   mascotIdle,
	} {
		m, _ := newTestModel(t, &testutil.ScriptedProvider{})
		m.playEgg(msg)
		if got := m.mascotState(); got != want {
			t.Errorf("%q: state = %v, want %v", msg, got, want)
		}
	}

	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.playEgg("let's party")
	m.inTurn = true
	if got := m.mascotState(); got != mascotPartying {
		t.Fatalf("state = %v, want partying over work", got)
	}
	m.pending = []PermissionRequestMsg{{Name: "write"}}
	if got := m.mascotState(); got != mascotWaiting {
		t.Fatalf("state = %v, want waiting over the egg", got)
	}
	m.pending = nil
	m.mascotTick += mascotEggTicks
	if got := m.mascotState(); got != mascotThinking {
		t.Fatalf("state = %v, want thinking once the egg is over", got)
	}
}

// The mascot sits over the bottom-right corner of the main area, inside its
// border and above the input box, in every layout; the frame keeps its
// size and borders.
func TestMascotOverlaysView(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	check := func(name string) {
		t.Helper()
		lines := strings.Split(stripANSI(m.View()), "\n")
		row := lines[len(lines)-6] // above the main box's bottom border, the 3-row input box and the statusline
		if !strings.HasSuffix(row, "wisp is idle│") {
			t.Fatalf("%s: mascot not in the bottom-right corner: %q", name, row)
		}
		if !strings.HasPrefix(lines[len(lines)-5], "╰") || !strings.HasPrefix(lines[len(lines)-4], "╭") {
			t.Fatalf("%s: borders broken:\n%s", name, strings.Join(lines, "\n"))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w != 100 {
				t.Fatalf("%s: row is %d cells, want 100: %q", name, w, l)
			}
		}
	}
	check("chat")
	m.debugOpen = true
	m.applyLayout()
	check("debug panel")
}

// A main area too small to hold the sprite inside its border is left alone.
func TestMascotOverlaySkipsTinyView(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	for _, main := range []string{"╭──╮\n╰──╯", "╭─╮\n│ │\n│ │\n╰─╯"} {
		if got := m.overlayMascot(main); got != main {
			t.Errorf("overlay changed %q to %q", main, got)
		}
	}
}

func TestMascotFrameWraps(t *testing.T) {
	for _, s := range []mascotState{mascotIdle, mascotThinking, mascotTool, mascotAgent} {
		n := len(mascotFrames[s])
		if mascotFrame(s, n) != mascotFrame(s, 0) || mascotFrame(s, -1) != mascotFrame(s, n-1) {
			t.Fatalf("state %v frame does not wrap", s)
		}
	}
}

// Every state's strip uploads as square frames with ids a 256-color
// foreground can carry.
func TestUploadMascotCutsEveryStrip(t *testing.T) {
	var b strings.Builder
	images := uploadMascot(&b, termColors{})
	if len(images) != len(mascotCaptions) {
		t.Fatalf("uploaded %d states, want %d", len(images), len(mascotCaptions))
	}
	for s, ids := range images {
		if len(ids) != 6 {
			t.Errorf("%s: %d frames, want 6", mascotCaptions[s], len(ids))
		}
		for _, id := range ids {
			if id < mascotImageBase || id > 255 {
				t.Errorf("%s: image id %d out of range", mascotCaptions[s], id)
			}
		}
	}
	if n := strings.Count(b.String(), "a=T,"); n != 6*len(mascotCaptions) {
		t.Errorf("%d uploads, want %d", n, 6*len(mascotCaptions))
	}
}

// With images uploaded, the mascot is an uncaptioned block of placeholder
// cells that switches image every other tick, painted over the main area's
// last inner rows at its right edge.
func TestImageMascotOverlay(t *testing.T) {
	mascotImages = map[mascotState][]int{mascotIdle: {100, 101, 102}}
	defer func() { mascotImages = nil }()

	if got := renderMascot(mascotIdle, 0); strings.Count(got, "\n") != mascotRows-1 || !strings.Contains(got, "38;5;100m") {
		t.Fatalf("frame 0 = %q", got)
	}
	if !strings.Contains(renderMascot(mascotIdle, 2), "38;5;101m") || !strings.Contains(renderMascot(mascotIdle, 7), "38;5;100m") {
		t.Fatal("frames don't advance every other tick and wrap")
	}
	if got := renderMascot(mascotTool, 0); strings.Contains(got, "\n") {
		t.Fatalf("state without frames should fall back to text: %q", got)
	}

	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	lines := strings.Split(stripANSI(m.View()), "\n")
	if strings.Contains(strings.Join(lines, "\n"), "wisp is") {
		t.Fatal("image mascot captioned")
	}
	bottom := len(lines) - 5 // the main box's bottom border
	for _, l := range lines[bottom-mascotRows : bottom] {
		i := strings.IndexRune(l, placeholder)
		if at := ansi.StringWidth(l[:max(i, 0)]); i < 0 || at != 100-1-mascotCols {
			t.Fatalf("mascot row starts at column %d, want %d:\n%s", at, 100-1-mascotCols, strings.Join(lines, "\n"))
		}
		if w := ansi.StringWidth(l); w != 100 || !strings.HasSuffix(l, "│") {
			t.Fatalf("row is %d cells or lost its border: %q", w, l)
		}
	}
	if !strings.HasPrefix(lines[bottom], "╰") {
		t.Fatalf("bottom border lost:\n%s", strings.Join(lines, "\n"))
	}
}

// While quiet, eggs play at random: never within mascotEggQuietTicks of
// activity or the last egg, never during a turn, and never the sleep one.
func TestMascotRandomEggs(t *testing.T) {
	roll := 0
	defer func(r func(int) int) { mascotRoll = r }(mascotRoll)
	mascotRoll = func(n int) int { return roll % n }

	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.mascotTick = mascotEggQuietTicks - 1
	m.maybeRandomEgg()
	if m.mascotState() != mascotIdle {
		t.Fatalf("egg %v before the quiet time", m.mascotState())
	}
	m.mascotTick++
	m.inTurn = true
	m.maybeRandomEgg()
	if m.mascotTick < m.eggUntil {
		t.Fatal("egg during a turn")
	}
	m.inTurn = false
	roll = 1
	m.maybeRandomEgg()
	if m.mascotTick < m.eggUntil {
		t.Fatal("egg despite a losing roll")
	}
	roll = 0
	m.maybeRandomEgg()
	if got := m.mascotState(); got != mascotEggs[0].state {
		t.Fatalf("state = %v, want the first egg", got)
	}
	m.mascotTick = m.eggUntil + mascotEggQuietTicks - 1
	m.maybeRandomEgg()
	if m.mascotTick < m.eggUntil {
		t.Fatal("egg too soon after the last one")
	}

	// A winning roll, then the pick: every index in turn.
	var rolls []int
	mascotRoll = func(n int) int { r := rolls[0]; rolls = rolls[1:]; return r % n }
	seen := map[mascotState]bool{}
	for i := range len(mascotEggs) {
		rolls = []int{0, i}
		m.mascotTick = 1000 * (i + 1)
		m.maybeRandomEgg()
		seen[m.egg] = true
	}
	if seen[mascotSleeping] || len(seen) != len(mascotEggs)-1 {
		t.Fatalf("random eggs %v, want every egg but sleeping", seen)
	}
}

// The transcript keeps clear of the mascot: a row below it for the text
// sprite, a gutter beside it for the image one, unless that would leave the
// chat too narrow.
func TestMascotRoom(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if cols, rows := m.mascotRoom(); cols != 0 || rows != 1 || m.chatWidth() != 94 {
		t.Fatalf("text sprite: room %d×%d, chat width %d", cols, rows, m.chatWidth())
	}
	mascotImages = map[mascotState][]int{mascotIdle: {100}}
	defer func() { mascotImages = nil }()
	if cols, rows := m.mascotRoom(); cols != mascotCols || rows != 0 || m.chatWidth() != 100-4-mascotCols {
		t.Fatalf("image sprite: room %d×%d, chat width %d", cols, rows, m.chatWidth())
	}
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 30})
	if cols, _ := m.mascotRoom(); cols != 0 || m.chatWidth() != 44 {
		t.Fatalf("narrow chat: gutter %d, chat width %d", cols, m.chatWidth())
	}
}
