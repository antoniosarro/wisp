package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
)

// Options is the static session info the UI shows.
type Options struct {
	WorkDir string // where file tools act, shown on approvals
}

// Model is the Bubble Tea frontend: a transcript viewport, a prompt input,
// and the state of the in-flight turn and its pending permission requests.
type Model struct {
	loop *core.Loop
	ctx  context.Context
	send func(tea.Msg)
	msgs <-chan tea.Msg
	opts Options

	viewport viewport.Model
	input    textarea.Model
	spin     spinner.Model
	approval viewport.Model // the front request's details

	width, height int
	ready         bool   // the viewport exists: a WindowSizeMsg has come
	autoScroll    bool   // follow new output; off once the user scrolls up
	notice        string // shown in the chat box's bottom border

	blocks        blockList
	blockLines    []lineRange // each block's lines in the last render
	selectedBlock int         // the block Ctrl+O acts on, or -1

	inputHistory []string  // submitted prompts, oldest first
	historyIndex int       // the entry shown, len(inputHistory) for the draft
	draft        string    // the unsent input, kept while browsing history
	quitArmed    time.Time // first Ctrl+C, which a second one within quitWindow completes

	inTurn     bool
	turnCancel context.CancelFunc
	turnDone   <-chan struct{} // closed when the latest turn's goroutine exits
	pending    []PermissionRequestMsg

	// Approval keys count only after the prompt has been visible, and the
	// user has paused typing, for approvalGuard: a prompt that appears
	// mid-sentence must not take the next letter as an answer.
	pendingSince time.Time
	lastKey      time.Time
	noting       bool        // typing a note to send with a denial
	noteDraft    string      // the draft set aside while noting
	approvalKey  approvalKey // what the approval viewport holds

	dirty        bool // transcript changed since the last render
	framePending bool // a frameMsg is scheduled
	quitting     bool
}

const inputPlaceholder = "ask wisp..."

// NewModel builds a Model around loop, replaying its History. msgs is the
// receive side of the channel send writes to.
func NewModel(ctx context.Context, loop *core.Loop, send func(tea.Msg), msgs <-chan tea.Msg, opts Options) *Model {
	ti := textarea.New()
	ti.Placeholder = inputPlaceholder
	ti.ShowLineNumbers = false
	ti.Prompt = "> "
	ti.MaxWidth = 0
	ti.MaxHeight = 0
	ti.SetHeight(1)
	ti.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	ti.FocusedStyle, ti.BlurredStyle = inputStyles()
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleSpinner

	m := &Model{
		loop:          loop,
		ctx:           ctx,
		send:          send,
		msgs:          msgs,
		opts:          opts,
		approval:      viewport.New(1, 1),
		input:         ti,
		spin:          sp,
		autoScroll:    true,
		selectedBlock: -1,
	}
	m.replayHistory(loop.History)
	return m
}

// Init starts the message pump, the spinner, and the cursor blink.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(listenForMsg(m.msgs), m.spin.Tick, m.input.Focus())
}

// listenForMsg waits for the next message from the turn goroutine or
// prompter; each handler re-issues it to keep the pump running.
func listenForMsg(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// Update applies msg.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.applyLayout()
		return m, nil
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		if m.inTurn { // running calls show the spinner
			if m.pruneStale() {
				m.applyLayout()
			}
			m.syncViewport()
		}
		return m, cmd
	case frameMsg:
		m.framePending = false
		if m.dirty {
			m.syncViewport()
		}
		return m, nil
	case TurnDoneMsg:
		m.finishTurn(msg.Err)
		return m, tea.Batch(listenForMsg(m.msgs), m.input.Focus())
	}
	if m.handleTurnMsg(msg) {
		return m, tea.Batch(listenForMsg(m.msgs), m.frame())
	}
	return m, m.updateInput(msg)
}

// handleTurnMsg applies a message from the pump other than TurnDoneMsg,
// reporting whether msg was one.
func (m *Model) handleTurnMsg(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case StreamMsg:
		m.blocks.appendEvent(model.Event(msg))
	case ToolResultMsg:
		m.blocks.resolve(msg.Call, msg.Result, msg.Err)
	case CompactMsg:
		m.applyCompaction(core.CompactEvent(msg))
	case PermissionRequestMsg:
		if msg.Context != nil && msg.Context.Err() != nil {
			return true
		}
		if len(m.pending) == 0 {
			m.pendingSince = time.Now()
		}
		m.pending = append(m.pending, msg)
		m.approval.GotoTop()
		m.applyLayout()
	default:
		return false
	}
	m.dirty = true
	return true
}

// applyCompaction notes a compaction's start and end in the transcript.
func (m *Model) applyCompaction(e core.CompactEvent) {
	text := "Compacting the conversation…"
	switch {
	case e.Done && e.Err != nil:
		text = "Compacted without a summary: " + e.Err.Error()
	case e.Done:
		text = "Compacted the conversation."
	}
	m.blocks = append(m.blocks, block{kind: blockNotice, text: text})
}

// updateInput forwards msg to the textarea, relaying out only when its
// height may have changed.
func (m *Model) updateInput(msg tea.Msg) tea.Cmd {
	// The input re-wraps all its text on every key, so a huge paste would
	// make typing crawl; a file serves it better.
	if k, ok := msg.(tea.KeyMsg); ok && k.Paste && len(string(k.Runes)) > maxPasteBytes {
		m.notice = fmt.Sprintf("Paste of %d KB not inserted: save it to a file and ask wisp to read it", len(string(k.Runes))/1024)
		return nil
	}
	lines := m.input.LineCount()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.LineCount() != lines {
		m.applyLayout()
	}
	return cmd
}

// submit starts a turn with the input, unless one is running.
func (m *Model) submit() tea.Cmd {
	input := strings.TrimSpace(m.input.Value())
	if input == "" {
		return nil
	}
	if m.inTurn {
		m.notice = "Still working: your draft stays here; press Enter again once the turn ends (Esc cancels the turn)"
		return nil
	}

	m.input.SetValue("")
	m.recordHistory(input)
	m.inTurn = true
	m.appendBlock(block{kind: blockUser, text: input})
	m.applyLayout()

	turnCtx, cancel := context.WithCancel(m.ctx)
	m.turnCancel = cancel
	m.turnDone = RunTurn(turnCtx, m.loop, input, m.send)
	return nil
}

// Wait blocks until the latest turn's goroutine has exited, so its final
// messages are stored before the caller closes the session store.
func (m *Model) Wait() {
	if m.turnDone == nil {
		return
	}
	select {
	case <-m.turnDone:
	case <-time.After(turnDrainTimeout): // a stream deaf to cancellation; the process is exiting
	}
}

// turnDrainTimeout bounds how long quitting waits for a cancelled turn to
// record its results.
const turnDrainTimeout = 5 * time.Second

// maxPasteBytes is the largest paste the input takes.
const maxPasteBytes = 100 << 10

// frameInterval bounds how often streamed output re-renders the transcript.
const frameInterval = 50 * time.Millisecond

// frameMsg asks for a render of what changed since the last one.
type frameMsg struct{}

// frame schedules a render of pending transcript changes, coalescing the
// many stream events that arrive within one frame.
func (m *Model) frame() tea.Cmd {
	if !m.dirty || m.framePending {
		return nil
	}
	m.framePending = true
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return frameMsg{} })
}

// cancelTurn interrupts the running turn and denies its pending approvals.
func (m *Model) cancelTurn() {
	m.turnCancel()
	for _, req := range m.pending {
		select {
		case req.Reply <- Answer{Decision: permission.Deny}:
		default:
		}
	}
	m.pending = nil
	m.stopNoting()
	m.applyLayout()
}

// finishTurn settles blocks left open by the turn and reports its error.
func (m *Model) finishTurn(err error) {
	m.inTurn = false
	if m.turnCancel != nil {
		m.turnCancel()
		m.turnCancel = nil
	}
	m.pending = nil
	m.stopNoting()
	m.blocks.settle(err)
	m.syncViewport()
}
