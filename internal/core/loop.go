// Package core implements the agent loop: stream a response, dispatch the
// tool calls it requests, feed results back, repeat until a plain answer.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// defaultMaxIterations bounds a turn's provider round-trips when the Loop
// sets no MaxIterations.
const defaultMaxIterations = 100

// ReminderPrefix starts user-role messages the harness writes itself, so
// frontends can hide them.
const ReminderPrefix = "[wisp reminder] "

// InterruptedReply stands in for the answer of a turn that never got one,
// so user and assistant messages keep alternating.
const InterruptedReply = "(no reply: the turn was interrupted)"

// InterruptedResult stands in for the result of a tool call that never
// finished.
const InterruptedResult = "interrupted before a result was received"

// truncatedReminder follows a response cut off at the output-token limit.
const truncatedReminder = "Your previous response hit the output length limit and was cut off; any tool calls in it were dropped. Do not repeat your reasoning. Act now: make the next tool call, or give a short final answer."

// wrapUpReminder asks for a summary once a turn runs out of steps.
const wrapUpReminder = "You have reached the step limit for this turn. Do not call tools. Summarize what you did, what you found, and what is left to do."

// RepeatedCallContent answers a read-only call identical to one already run
// in the turn, instead of running it again.
const RepeatedCallContent = "Not run again: an identical call already ran in this turn, and no tool that changes anything has run since, so its result above still holds. Use that result, or try something different."

// MessageStore persists messages as they're appended to History.
type MessageStore interface {
	AppendMessage(sessionID string, msg model.Message) error
}

// Loop runs turns against a Provider. Optional callbacks let a frontend
// render streamed events, tool results, and per-step stats as they happen.
// A Loop runs one turn at a time; it is not safe for concurrent use.
type Loop struct {
	Provider model.Provider
	Tools    *tool.Registry // nil means no tools
	System   string         // sent ahead of History on every request, never persisted
	History  []model.Message

	Store     MessageStore // nil means in-memory only
	SessionID string

	MaxIterations int           // provider round-trips per Run; <= 0 uses the default
	ContextWindow int           // model's window in tokens; 0 if unknown
	MaxOutput     int           // model's output-token cap; 0 if unknown
	Price         model.Pricing // the model's list price, for requests the endpoint doesn't bill
	NoTools       bool          // the model can't call tools: send none

	// FinishCheck, if set, runs when the model gives a final answer, with
	// this turn's messages. A non-empty result is sent back as a reminder
	// and the turn continues; it applies at most once per Run.
	FinishCheck func(turn []model.Message) string

	OnEvent      func(model.Event)                        // every streamed event, as it arrives
	OnToolResult func(model.ToolCall, tool.Result, error) // each call as it finishes, skipped ones included
	OnStats      func(StepStats)                          // after each request, when its stream ends

	stats  StepStats       // session-cumulative totals
	ran    map[string]bool // read-only calls run this turn since the last risky one (callKey)
	tokens tokenCache      // local token estimates (budget.go)
}

// Run appends userInput as a user turn and drives the provider/tool loop
// until a plain-text answer comes back. Past MaxIterations, it asks for a
// summary of the work so far instead of dropping it (wrapUp).
func (l *Loop) Run(ctx context.Context, userInput string) (answer string, err error) {
	if err := l.closeOpenTurn(); err != nil {
		return "", err
	}
	l.dropImages()
	l.restoreSpills()
	turnStart := len(l.History)
	l.ran = map[string]bool{}
	if err := l.appendAndPersist(model.Message{Role: model.RoleUser, Content: userInput}); err != nil {
		return "", err
	}
	reminded := false

	maxIter := l.MaxIterations
	if maxIter <= 0 {
		maxIter = defaultMaxIterations
	}

	for range maxIter {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		text, toolCalls, truncated, err := l.stepFitting(ctx, "")
		if err != nil {
			return "", err
		}

		if err := l.appendAndPersist(model.Message{Role: model.RoleAssistant, Content: text, ToolCalls: toolCalls}); err != nil {
			return "", err
		}

		// Cut off at the output limit: its tool calls were dropped, so
		// ask the model to act instead of starting over.
		if truncated {
			if err := l.appendReminder(truncatedReminder); err != nil {
				return "", err
			}
			continue
		}

		if len(toolCalls) == 0 {
			if reminder := l.finishReminder(turnStart, reminded); reminder != "" {
				reminded = true
				if err := l.appendReminder(reminder); err != nil {
					return "", err
				}
				continue
			}
			return text, nil
		}

		if err := l.dispatchAndAppend(ctx, toolCalls); err != nil {
			return "", err
		}
	}

	return l.wrapUp(ctx, maxIter)
}

// wrapUp asks for a final summary once the step limit is reached, so the
// work done so far isn't lost with the turn. It still fails the turn when
// no summary comes back.
func (l *Loop) wrapUp(ctx context.Context, maxIter int) (string, error) {
	limitErr := fmt.Errorf("exceeded %d iterations without a final answer", maxIter)
	if err := l.appendReminder(wrapUpReminder); err != nil {
		return "", err
	}
	text, _, _, err := l.stepFitting(ctx, "none")
	if err != nil {
		return "", errors.Join(limitErr, err)
	}
	if strings.TrimSpace(text) == "" {
		return "", limitErr
	}
	// Any tool calls are dropped: they were not allowed and will not run.
	if err := l.appendAndPersist(model.Message{Role: model.RoleAssistant, Content: text}); err != nil {
		return "", err
	}
	return text, nil
}

// appendReminder appends a harness message the model reads as the user's.
func (l *Loop) appendReminder(text string) error {
	return l.appendAndPersist(model.Message{Role: model.RoleUser, Content: ReminderPrefix + text})
}

// finishReminder is FinishCheck's reminder for the turn begun at
// turnStart, or "" when there is none or the turn was reminded already.
func (l *Loop) finishReminder(turnStart int, reminded bool) string {
	if reminded || l.FinishCheck == nil {
		return ""
	}
	return l.FinishCheck(l.History[turnStart:])
}

// closeOpenTurn repairs what an interrupted or crashed turn left behind:
// tool calls without results get placeholder results, and a trailing user
// message gets a placeholder answer. Strict backends reject either.
func (l *Loop) closeOpenTurn() error {
	answered := map[string]bool{}
	// Only the last assistant message can have open calls: walk back to it
	// through the tool results that follow it.
	for i := len(l.History) - 1; i >= 0; i-- {
		msg := l.History[i]
		if msg.Role == model.RoleTool {
			answered[msg.ToolCallID] = true
			continue
		}
		for _, call := range msg.ToolCalls {
			if !answered[call.ID] {
				err := l.appendAndPersist(model.Message{Role: model.RoleTool, Content: InterruptedResult, ToolCallID: call.ID, IsError: true})
				if err != nil {
					return err
				}
			}
		}
		break
	}
	if n := len(l.History); n > 0 && l.History[n-1].Role == model.RoleUser {
		return l.appendAndPersist(model.Message{Role: model.RoleAssistant, Content: InterruptedReply})
	}
	return nil
}

// dropImages detaches images from earlier turns: the model has seen them,
// and resending them with every request costs context.
func (l *Loop) dropImages() {
	for i := range l.History {
		if msg := &l.History[i]; len(msg.Images) > 0 {
			msg.Images = nil
			msg.Content += " [image no longer attached; read the file again to see it]"
			l.tokens.counted = 0 // an earlier message changed: count again
		}
	}
}

// appendAndPersist writes through immediately so a crash mid-turn keeps prior progress.
func (l *Loop) appendAndPersist(msg model.Message) error {
	l.History = append(l.History, msg)
	if l.Store == nil {
		return nil
	}
	if err := l.Store.AppendMessage(l.SessionID, msg); err != nil {
		return fmt.Errorf("persisting message: %w", err)
	}
	return nil
}

// Messages returns what a request sends: the system prompt, then History
// with masked output replaced by its stand-in.
func (l *Loop) Messages() []model.Message {
	msgs := make([]model.Message, 0, len(l.History)+1)
	if l.System != "" {
		msgs = append(msgs, model.Message{Role: model.RoleSystem, Content: l.System})
	}
	for _, msg := range l.History {
		msgs = append(msgs, sent(msg))
	}
	return msgs
}

// stepFitting runs step, first masking old tool output when the request
// would take History past the mask trigger (Fit). When the backend rejects
// a request as too long anyway, it learns the window from the error if the
// server states it, then retries after each of: masking down to the
// target, and masking everything it can.
func (l *Loop) stepFitting(ctx context.Context, toolChoice string) (string, []model.ToolCall, bool, error) {
	if err := l.Fit(ctx); err != nil {
		return "", nil, false, err
	}
	text, calls, truncated, err := l.step(ctx, toolChoice)
	for attempt := 0; attempt < 2 && errors.Is(err, model.ErrContextOverflow); attempt++ {
		if window := windowFromError(err); window > 0 {
			l.ContextWindow = window
		}
		var changed bool
		switch attempt {
		case 0:
			changed = l.mask(l.historyBudget()*maskTargetPct/100, true, 0)
		default:
			// The estimate is off, or the protected steps alone overflow.
			changed = l.mask(0, false, 0)
		}
		if ctx.Err() != nil {
			return "", nil, false, ctx.Err()
		}
		if changed {
			text, calls, truncated, err = l.step(ctx, toolChoice)
		}
	}
	if errors.Is(err, model.ErrContextOverflow) {
		err = fmt.Errorf("%w; the conversation no longer fits, start a new session", err)
	}
	return text, calls, truncated, err
}

// step streams one provider response and returns its answer text, tool
// calls, and whether it was cut off at the output-token limit. toolChoice
// is sent only with tools: "none" forbids calls, "" leaves it to the model.
func (l *Loop) step(ctx context.Context, toolChoice string) (_ string, _ []model.ToolCall, truncated bool, _ error) {
	req := model.Request{Messages: l.Messages(), Tools: l.requestTools()}
	if req.Tools != nil {
		req.ToolChoice = toolChoice
	}

	start := time.Now()
	events, err := l.Provider.Stream(ctx, req)
	if err != nil {
		return "", nil, false, fmt.Errorf("starting stream: %w", err)
	}

	var text, reasoning, toolCallText strings.Builder
	var toolCalls []model.ToolCall
	var usage *model.Usage
	for e := range events {
		if l.OnEvent != nil {
			l.OnEvent(e)
		}
		switch e.Kind {
		case model.EventTextDelta:
			text.WriteString(e.Text)
		case model.EventReasoningDelta:
			reasoning.WriteString(e.Reasoning)
		case model.EventReclassify:
			// The text so far was reasoning, not the answer.
			reasoning.WriteString(text.String())
			text.Reset()
		case model.EventToolCall:
			if e.ToolCall != nil {
				toolCalls = append(toolCalls, *e.ToolCall)
				toolCallText.WriteString(e.ToolCall.Name)
				toolCallText.Write(e.ToolCall.Args)
			}
		case model.EventDone:
			usage, truncated = e.Usage, e.Truncated
		case model.EventError:
			return "", nil, false, fmt.Errorf("stream error: %w", e.Err)
		}
	}
	// A cancelled stream closes without EventDone or EventError.
	if err := ctx.Err(); err != nil {
		return "", nil, false, err
	}

	if usage != nil {
		l.calibrate(usage.PromptTokens, l.fixedTokens()+l.historyTokens())
	}
	l.recordStats(response{
		reasoning: reasoning.String(),
		answer:    text.String(),
		toolCalls: toolCallText.String(),
		usage:     usage,
		duration:  time.Since(start),
	})
	return text.String(), toolCalls, truncated, nil
}

// dispatchAndAppend appends one tool-role message per call, in request order.
// A read-only call identical to one already run in the turn, with no risky
// call in between, would return the same thing: it gets RepeatedCallContent
// instead of running, which stops small models calling the same tool in a
// loop and keeps the repeated output out of context.
func (l *Loop) dispatchAndAppend(ctx context.Context, toolCalls []model.ToolCall) error {
	if l.ran == nil {
		l.ran = map[string]bool{}
	}
	var run []model.ToolCall
	repeated := map[string]bool{}
	for _, call := range toolCalls {
		t, ok := l.Tools.Get(call.Name)
		switch {
		case !ok: // runs, so Dispatch reports it as unknown
		case tool.IsRisky(t, call.Args):
			clear(l.ran) // it may change what reads return
		case l.ran[callKey(call)]:
			repeated[call.ID] = true
			continue
		default:
			l.ran[callKey(call)] = true
		}
		run = append(run, call)
	}

	results := tool.Dispatch(ctx, l.Tools, run, func(r tool.CallResult) {
		if l.OnToolResult != nil {
			l.OnToolResult(r.ToolCall, r.Result, r.Err)
		}
	})
	byID := make(map[string]tool.CallResult, len(results))
	for _, r := range results {
		byID[r.ToolCall.ID] = r
	}
	for _, call := range toolCalls {
		r := byID[call.ID]
		if repeated[call.ID] {
			r = tool.CallResult{ToolCall: call, Result: tool.Result{Content: RepeatedCallContent, IsError: true}}
			if l.OnToolResult != nil {
				l.OnToolResult(call, r.Result, nil)
			}
		}
		content, images := r.Result.Content, r.Result.Images
		if r.Err != nil {
			content, images = r.Err.Error(), nil
		}
		if err := l.appendAndPersist(model.Message{
			Role:       model.RoleTool,
			Content:    l.fitResult(content),
			ToolCallID: r.ToolCall.ID,
			IsError:    r.Err != nil || r.Result.IsError,
			Images:     images,
		}); err != nil {
			return err
		}
	}
	return nil
}

// callKey identifies a call by tool and arguments, the arguments
// re-encoded so formatting and key order don't matter.
func callKey(call model.ToolCall) string {
	var v any
	if json.Unmarshal(call.Args, &v) == nil {
		if b, err := json.Marshal(v); err == nil {
			return call.Name + "\x00" + string(b)
		}
	}
	return call.Name + "\x00" + string(call.Args)
}
