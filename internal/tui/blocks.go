package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/tool"
)

// blockKind is what a block shows.
type blockKind int

const (
	blockUser blockKind = iota
	blockReasoning
	blockAnswer
	blockToolCall
	blockTurnError
	blockNotice
	blockCompaction // text is empty while it runs; detail is the summary
	blockContext    // usage is what /context showed
)

// toolStatus is where a tool call block's call stands.
type toolStatus int

const (
	toolRunning toolStatus = iota
	toolOK
	toolFailed
	toolDenied
)

// block is one renderable piece of the transcript, kept structured so it
// can be collapsed, updated in place, and styled per kind.
type block struct {
	cached      string // the last render, at cachedWidth
	cachedWidth int
	kind        blockKind
	text        string // user, answer, notice, turnError
	detail      string // compaction: the summary the model now sees
	usage       *contextUsage
	expanded    bool
	// stream accumulates a streaming answer or reasoning block; text or
	// reasoningText is its String(), which is O(1), so deltas don't recopy
	// the whole text. A pointer, so copying the block is safe.
	stream *strings.Builder

	reasoningText  string
	reasoningDone  bool
	reasoningStart time.Time
	reasoningTime  time.Duration // set once reasoning ends

	// A tool-call block also holds the result, matched by toolCallID.
	toolCallID string
	toolName   string
	toolArgs   json.RawMessage
	toolStatus toolStatus
	toolResult string
	resolved   bool          // the result arrived, or never will: nothing else may take it
	toolStart  time.Time     // zero for replayed calls
	toolTime   time.Duration // set once the result arrives
}

// invalidate drops b's cached render after it changed.
func (b *block) invalidate() { b.cached = "" }

// active blocks animate a spinner, so they are never served from cache.
func (b *block) active() bool {
	return (b.kind == blockReasoning && !b.reasoningDone) || (b.kind == blockToolCall && b.toolStatus == toolRunning) || (b.kind == blockCompaction && b.text == "")
}

// collapsible blocks toggle between a summary and their full content.
func (b *block) collapsible() bool {
	switch b.kind {
	case blockReasoning:
		return true
	case blockToolCall:
		return (b.toolStatus == toolOK || b.toolStatus == toolFailed) && b.toolResult != ""
	case blockCompaction:
		return b.detail != ""
	}
	return false
}

// copyText is what Ctrl+Y copies of b: what was said or run, not its
// frame. It is cleaned like what the screen shows: the clipboard ends up
// pasted into terminals, where an escape sequence in a model's text could
// end a bracketed paste and run what follows.
func (b *block) copyText() string {
	var text string
	switch b.kind {
	case blockReasoning:
		text = b.reasoningText
	case blockToolCall:
		text = b.toolName + " " + string(b.toolArgs) + "\n" + b.toolResult
	case blockCompaction:
		text = b.text + "\n\n" + b.detail
	case blockContext:
		return ansi.Strip(renderContextBlock(*b.usage, 100)) // rendered, so already clean
	default:
		text = b.text
	}
	return sanitize(text)
}

// blockList is one conversation's transcript.
type blockList []block

// appendEvent folds one streamed event into the transcript.
func (l *blockList) appendEvent(e model.Event) {
	if e.Kind != model.EventReasoningDelta {
		l.closeReasoning()
	}
	switch e.Kind {
	case model.EventReasoningDelta:
		b := l.open(blockReasoning)
		if b.reasoningStart.IsZero() {
			b.reasoningStart = time.Now()
		}
		b.reasoningText = b.write(b.reasoningText, e.Reasoning)
		b.invalidate()
	case model.EventTextDelta:
		b := l.open(blockAnswer)
		b.text = b.write(b.text, e.Text)
		b.invalidate()
	case model.EventReclassify:
		// The answer so far was reasoning whose opening tag was in the
		// prompt. Only this response's text is, and a response's text is
		// the last block: tool calls end a response.
		if b := l.last(blockAnswer); b != nil {
			*b = block{kind: blockReasoning, reasoningText: b.text, reasoningStart: time.Now()}
		}
	case model.EventToolCall:
		if e.ToolCall != nil {
			b := toolCallBlock(*e.ToolCall)
			b.toolStart = time.Now()
			*l = append(*l, b)
		}
	}
}

// write appends delta to text through the block's stream builder.
func (b *block) write(text, delta string) string {
	if b.stream == nil {
		b.stream = &strings.Builder{}
		b.stream.WriteString(text)
	}
	b.stream.WriteString(delta)
	return b.stream.String()
}

// open returns the last block if it has kind, else appends a new one, so
// consecutive deltas accumulate into one block.
func (l *blockList) open(kind blockKind) *block {
	if b := l.last(kind); b != nil {
		return b
	}
	*l = append(*l, block{kind: kind})
	return &(*l)[len(*l)-1]
}

// last returns the last block if it has kind.
func (l blockList) last(kind blockKind) *block {
	if len(l) == 0 || l[len(l)-1].kind != kind {
		return nil
	}
	return &l[len(l)-1]
}

// closeReasoning ends the reasoning block the model was writing, if any.
func (l blockList) closeReasoning() {
	if b := l.last(blockReasoning); b != nil && !b.reasoningDone {
		b.finishReasoning()
	}
}

// finishReasoning marks b's reasoning done and times it.
func (b *block) finishReasoning() {
	b.reasoningDone = true
	b.reasoningTime = time.Since(b.reasoningStart)
	b.invalidate()
}

// resolve records a tool call's outcome on its block: the first call
// with its ID still waiting for one. Matching unresolved calls only, and
// the first of them, keeps results in order when a provider sends
// parallel calls without IDs, or reuses IDs from turn to turn. A denied
// call still waits: its result carries the user's note.
func (l blockList) resolve(call model.ToolCall, res tool.Result, err error) {
	var b *block
	for i := range l {
		if l[i].kind == blockToolCall && !l[i].resolved && l[i].toolCallID == call.ID {
			b = &l[i]
			break
		}
	}
	if b == nil {
		return
	}
	b.invalidate()
	b.resolved = true
	if !b.toolStart.IsZero() {
		b.toolTime = time.Since(b.toolStart)
	}
	switch {
	case err != nil:
		b.toolStatus = toolFailed
		b.toolResult = err.Error()
	case res.IsError && strings.HasPrefix(res.Content, permission.DeniedContent):
		b.toolStatus = toolDenied
		b.toolResult = strings.TrimPrefix(strings.TrimPrefix(res.Content, permission.DeniedContent), ". ")
	case res.IsError:
		b.toolStatus = toolFailed
		b.toolResult = res.Content
	default:
		b.toolStatus = toolOK
		b.toolResult = res.Content
	}
}

// settle closes what a stopped conversation left open and records why it stopped.
func (l *blockList) settle(err error) {
	for i := range *l {
		b := &(*l)[i]
		if b.kind == blockReasoning && !b.reasoningDone {
			b.finishReasoning()
		}
		if b.kind == blockToolCall && !b.resolved {
			b.resolved = true
			if b.toolStatus == toolRunning {
				b.toolTime = time.Since(b.toolStart)
				b.toolStatus = toolFailed
				b.toolResult = "interrupted before a result was received"
				b.invalidate()
			}
		}
		if b.kind == blockCompaction && b.text == "" {
			b.text = "Compaction stopped; the context is unchanged"
			b.invalidate()
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		*l = append(*l, block{kind: blockTurnError, text: "interrupted"})
	case err != nil:
		*l = append(*l, block{kind: blockTurnError, text: err.Error()})
	}
}

// appendBlock adds b to the chat and scrolls to it.
func (m *Model) appendBlock(b block) {
	m.blocks = append(m.blocks, b)
	m.autoScroll = true
}

// replayHistory rebuilds the transcript from saved messages, marking where
// each compaction began the model's view. What the harness wrote itself,
// reminders and stand-ins for missing replies, is the model's business,
// not the user's.
func (m *Model) replayHistory(history []model.Message) {
	marks := m.loop.Compactions
	m.loadPastes()
	for i, msg := range history {
		for len(marks) > 0 && marks[0].FirstKept <= i {
			m.blocks = append(m.blocks, compactionBlock(marks[0], marks[0].Text()))
			marks = marks[1:]
		}
		switch msg.Role {
		case model.RoleUser:
			if strings.HasPrefix(msg.Content, core.ReminderPrefix) {
				continue
			}
			prompt := m.shownPrompt(msg.Content)
			m.blocks = append(m.blocks, block{kind: blockUser, text: prompt})
			m.recordHistory(prompt)
		case model.RoleAssistant:
			m.blocks.orphan() // a reused call ID must not match an older call
			if msg.Content != "" && msg.Content != core.InterruptedReply {
				m.blocks = append(m.blocks, block{kind: blockAnswer, text: msg.Content})
			}
			for _, call := range msg.ToolCalls {
				if call.Name == "todo" {
					m.setTodos(call.Args)
				}
				m.blocks = append(m.blocks, toolCallBlock(call))
			}
		case model.RoleTool:
			m.blocks.resolve(model.ToolCall{ID: msg.ToolCallID}, tool.Result{Content: msg.Content, IsError: msg.IsError}, nil)
		}
	}
	m.blocks.orphan()
}

// orphan fails replayed calls that no saved result resolved.
func (l blockList) orphan() {
	for i := range l {
		if b := &l[i]; b.kind == blockToolCall && !b.resolved {
			b.resolved = true
			b.toolStatus = toolFailed
			b.toolResult = "No saved result; execution may have been interrupted."
		}
	}
}

// appendStream folds a main-agent event into the main chat.
func (m *Model) appendStream(e StreamMsg) {
	m.blocks.appendEvent(model.Event(e))
	if c := e.ToolCall; e.Kind == model.EventToolCall && c != nil && c.Name == "todo" {
		m.setTodos(c.Args)
	}
}

// toolCallBlock is a running call's block.
func toolCallBlock(call model.ToolCall) block {
	return block{kind: blockToolCall, toolCallID: call.ID, toolName: call.Name, toolArgs: call.Args}
}

// answerPermission records an approval decision on the call's block: a
// denial marks it, an approval restarts its timer so the wait for the
// user isn't counted. Requests without a call ID match by name and args.
func (m *Model) answerPermission(req PermissionRequestMsg, allow bool) {
	var b *block
	for i := range m.blocks {
		c := &m.blocks[i]
		if c.kind != blockToolCall || c.toolStatus != toolRunning {
			continue
		}
		if req.CallID != "" && c.toolCallID == req.CallID || req.CallID == "" && c.toolName == req.Name && string(c.toolArgs) == string(req.Args) {
			b = c
			break
		}
	}
	switch {
	case b == nil:
	case allow:
		b.toolStart = time.Now()
	default:
		b.toolStatus = toolDenied
		b.invalidate()
	}
}

// toggleLastReasoning expands or collapses the latest reasoning shown.
func (m *Model) toggleLastReasoning() {
	shown := *m.shown()
	for i := len(shown) - 1; i >= 0; i-- {
		if shown[i].kind == blockReasoning {
			m.toggleBlock(i)
			return
		}
	}
}

// toggleBlock expands or collapses shown block i. Toggling the last block while
// following the output keeps following it; any other block stays in place.
func (m *Model) toggleBlock(i int) {
	shown := *m.shown()
	b := &shown[i]
	b.expanded = !b.expanded
	b.invalidate()
	if i == len(shown)-1 && (m.autoScroll || m.viewport.AtBottom()) {
		m.autoScroll = true
		m.syncViewport()
		return
	}
	m.autoScroll = false
	m.syncViewport()
	m.autoScroll = m.viewport.AtBottom()
}

// selectBlock moves the selection to the next or previous block, starting
// from the last, and scrolls it into view.
func (m *Model) selectBlock(next bool) {
	n := len(*m.shown())
	if n == 0 {
		return
	}
	switch {
	case m.selectedBlock < 0:
		m.selectedBlock = n - 1
	case next:
		m.selectedBlock = min(n-1, m.selectedBlock+1)
	default:
		m.selectedBlock = max(0, m.selectedBlock-1)
	}
	m.autoScroll = false
	m.syncViewport()
	// Before the first render (no window size yet) there are no line
	// ranges to scroll to; the selection still applies.
	if m.selectedBlock < len(m.blockLines) {
		m.viewport.SetYOffset(m.blockLines[m.selectedBlock].start)
	}
}

// selectedBlockOrLast returns the selected block, selecting the last one
// if none is.
func (m *Model) selectedBlockOrLast() *block {
	if m.selectedBlock < 0 {
		m.selectBlock(false)
	}
	if m.selectedBlock < 0 {
		return nil
	}
	return &(*m.shown())[m.selectedBlock]
}

// expandSelected opens the selected block: a sub-agent's conversation, or
// the block's full content.
func (m *Model) expandSelected() {
	if b := m.selectedBlockOrLast(); b != nil {
		m.activate(m.selectedBlock)
	}
}

// activate acts on shown block i as a click would.
func (m *Model) activate(i int) {
	if runID, ok := m.runOf(i); ok {
		m.openRun(runID)
		return
	}
	if (*m.shown())[i].collapsible() {
		m.toggleBlock(i)
	}
}

// interactive reports whether clicking shown block i does something.
func (m *Model) interactive(i int) bool {
	if _, ok := m.runOf(i); ok {
		return true
	}
	return (*m.shown())[i].collapsible()
}

// runOf returns the sub-agent run shown block i started, if it is an agent
// call whose conversation can be opened.
func (m *Model) runOf(i int) (int64, bool) {
	b := &(*m.shown())[i]
	if b.kind != blockToolCall || b.toolName != agent.ToolName {
		return 0, false
	}
	if run := m.agentRunFor(b.toolCallID); run != nil && m.runViews[run.RunID] != nil {
		return run.RunID, true
	}
	return 0, false
}
