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

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
)

// Options is the session info the UI shows and changes.
type Options struct {
	Model   model.Info   // the current model; empty ID until one is chosen
	Models  []model.Info // the endpoint's models, offered when Model is empty
	BaseURL string
	WorkDir string // where file tools act, shown on approvals and the splash
	Suggest bool   // ask for a suggested next message after each reply
	// TraceURL, when --trace serves the trace page, is shown on the splash.
	TraceURL string
	// Update, when a newer release exists, is its version, shown on the splash.
	Update string
	// OnModel, if set, configures loop for a newly described model and
	// returns the details to show (with any overrides applied).
	OnModel func(loop *core.Loop, info model.Info) model.Info
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
	help     viewport.Model // the help modal's scrolling body
	debug    viewport.Model // the debug panel, full screen on a narrow terminal

	width, height int
	splash        string    // the rendered splash, for splashKey
	splashKey     splashKey // what splash shows
	ready         bool      // the viewport exists: a WindowSizeMsg has come
	autoScroll    bool      // follow new output; off once the user scrolls up
	notice        string    // shown in the chat box's bottom border

	blocks        blockList
	blockLines    []lineRange // each block's lines in the last render
	selectedBlock int         // the block Ctrl+O acts on, or -1
	hoverBlock    int         // under the mouse pointer, or -1
	content       string      // the last rendered transcript
	sel           textSelection
	mouseX        int
	mouseY        int

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

	stats     core.StepStats // the latest step's, from the loop
	cost      costSummary    // this session's spend, priced request by request
	debugOpen bool
	todos     []core.Todo // the model's latest task list
	todoOpen  bool

	// The mascot animates on the spinner tick. mascot is the state shown
	// since tick mascotSince; mascotWant is the state wanted for the last
	// mascotWantTicks ticks (see stepMascot). turnEndTick and
	// keyTick are the ticks of the last turn's end and the last key press;
	// egg plays until tick eggUntil.
	mascotTick      int
	mascot          mascotState
	mascotSince     int
	mascotWant      mascotState
	mascotWantTicks int
	turnEndTick     int
	keyTick         int
	egg             mascotState
	eggUntil        int
	// mascotPad is the blank rows applyLayout keeps below the transcript
	// for the text mascot (see mascotRoom).
	mascotPad int

	agentsOpen bool
	agentRuns  []agent.Event
	runViews   map[int64]*blockList // each sub-agent run's conversation
	viewing    int64                // run on screen, 0 for the main chat

	suggestion    string // proposed next message, accepted with →
	suggestGen    int    // identifies the latest suggestion request
	suggestCancel context.CancelFunc

	helpOpen bool
	modal       *picker // a list to choose from, over the chat

	knownModels []model.Info  // the endpoint's models, as last listed
	argCache    []pickerItem  // what the popup suggests as arguments, while open
	argCacheFor string        // the command argCache holds arguments for
	redescribed bool          // asked again after a turn loaded the model
	laterModel  *modelInfoMsg // a description that came during a turn, applied after it

	cmdIndex     int    // the command chosen in the suggestion popup
	cmdDismissed string // input for which Esc closed the popup
	popupRows    int    // rows the popup took at the last layout

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
		help:          viewport.New(1, 1),
		debug:         viewport.New(1, 1),
		knownModels:   opts.Models,
		input:         ti,
		spin:          sp,
		autoScroll:    true,
		runViews:      map[int64]*blockList{},
		selectedBlock: -1,
		hoverBlock:    -1,
	}
	m.replayHistory(loop.History)
	if opts.Model.ID == "" {
		m.showModels(opts.Models, "Choose a model for this session:")
	}
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
		m.keyTick = m.mascotTick
		cmd := m.handleKey(msg)
		m.fitPopup()
		return m, cmd
	case tea.MouseMsg:
		return m, m.handleMouse(msg)
	case clipboardResultMsg:
		if msg.err != nil { // kept in the chat: a border label is easy to miss
			m.notify("Copy failed: " + msg.err.Error())
			return m, nil
		}
		m.notice = msg.notice
		return m, nil
	case modelsMsg:
		return m, m.handleModels(msg)
	case modelInfoMsg:
		m.useModel(msg)
		return m, nil
	case suggestionMsg:
		m.showSuggestion(msg)
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		m.mascotTick++
		m.stepMascot()
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
		cmds := []tea.Cmd{listenForMsg(m.msgs), m.input.Focus()}
		if msg.Err == nil && m.opts.Suggest && !msg.Compact {
			cmds = append(cmds, m.requestSuggestion())
		}
		// Ollama and LM Studio load a model on first use; only then do
		// they report the context it runs with. llama-swap's servers are
		// asked about effort levels only once they run.
		unknown := m.loop.ContextWindow == 0 || m.opts.Model.Local && m.opts.Model.Efforts == 0
		if msg.Err == nil && unknown && !m.redescribed {
			m.redescribed = true
			cmds = append(cmds, m.describeModel(m.opts.Model.ID, true))
		}
		return m, tea.Batch(cmds...)
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
		m.appendStream(msg)
	case ToolResultMsg:
		m.blocks.resolve(msg.Call, msg.Result, msg.Err)
	case CompactMsg:
		m.applyCompaction(core.CompactEvent(msg))
	case StatsMsg:
		m.stats = core.StepStats(msg)
		m.cost.add(m.stats, m.opts.Model)
		if m.debugOpen { // stats show nowhere else
			m.refreshDebug()
		}
	case UpdateMsg:
		m.opts.Update = string(msg)
		m.applyLayout()
	case AgentMsg:
		m.updateAgentRun(agent.Event(msg))
	case AgentTraceMsg:
		m.applyTrace(agent.Trace(msg))
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

// submit runs the input as a command, or starts a turn with it unless one
// is running or no model is chosen.
func (m *Model) submit() tea.Cmd {
	input := strings.TrimSpace(m.input.Value())
	if input == "" {
		return nil
	}
	m.clearSuggestion()
	m.closeHelp()
	if m.viewing != 0 { // show where the message goes
		m.viewMain()
	}
	if strings.HasPrefix(input, "/") {
		m.input.SetValue("")
		m.recordHistory(input)
		cmd, ok := m.runCommand(input)
		if !ok {
			m.appendBlock(block{kind: blockTurnError, text: "unknown command: " + input})
		}
		m.applyLayout()
		return cmd
	}
	if m.inTurn {
		m.notice = "Still working: your draft stays here; press Enter again once the turn ends (Esc cancels the turn)"
		return nil
	}
	if m.opts.Model.ID == "" {
		m.notify(msgChooseModel)
		return nil
	}

	m.input.SetValue("")
	m.recordHistory(input)
	m.inTurn = true
	m.playEgg(input)
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
	m.turnEndTick = m.mascotTick
	if later := m.laterModel; later != nil {
		m.laterModel = nil
		defer m.useModel(*later)
	}
	if m.turnCancel != nil {
		m.turnCancel()
		m.turnCancel = nil
	}
	m.pending = nil
	m.stopNoting()
	m.blocks.settle(err)
	m.syncViewport()
}
