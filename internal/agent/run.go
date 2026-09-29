package agent

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/textfmt"
	"github.com/antoniosarro/wisp/internal/tool"
)

// Status is a sub-agent run's lifecycle stage.
type Status int

const (
	Waiting Status = iota // queued behind the parallel limit
	Running
	Done
	Failed
)

// Step is one tool call a sub-agent made.
type Step struct {
	ID     string
	Name   string
	Args   json.RawMessage
	Status string // "running", "ok", "failed"
	Output string // the tool's result, capped at maxStepOutput
}

const (
	maxStepOutput = 4000
	// textEvery throttles snapshots while text streams in.
	textEvery = 200 * time.Millisecond
)

// Event is a snapshot of a run, sent whenever it changes meaningfully.
type Event struct {
	RunID    int64
	CallID   string // the main agent's tool call that started the run
	Agent    string
	Task     string
	Status   Status
	Activity string // "thinking", "writing report", or "" while a step runs
	Steps    []Step
	Thinking string // reasoning since the last tool call
	Text     string // answer text since the last tool call; the report once done
	Started  time.Time
	Took     time.Duration

	PromptTokens     int
	CompletionTokens int
	Cost             float64 // so far: billed where the endpoint says, else at the model's list price
	CostEstimated    bool    // some of Cost is at list price

	Report string
	Err    error
}

// Trace is one raw step of a run's conversation, for frontends that show
// it in full: a streamed event or a finished tool call.
type Trace struct {
	RunID  int64
	Event  *model.Event
	Result *tool.CallResult
}

// run tracks one sub-agent execution and reports snapshots to the observer.
type run struct {
	mu       sync.Mutex
	ev       Event
	observer func(Event)
	trace    func(Trace)
	lastEmit time.Time
	// Streamed since the last tool call; copied into ev when emitting, so
	// long streams aren't re-concatenated on every delta.
	thinking, text []byte
}

// update applies change and reports a snapshot of the run.
func (r *run) update(change func(*Event)) {
	r.mu.Lock()
	change(&r.ev)
	r.lastEmit = time.Now()
	snapshot := r.ev
	snapshot.Steps = append([]Step(nil), r.ev.Steps...)
	r.mu.Unlock()
	if r.observer != nil {
		r.observer(snapshot)
	}
}

// emit reports the run as it is.
func (r *run) emit() { r.update(func(*Event) {}) }

// onEvent accumulates streamed text, reporting activity changes at once
// and text at most every textEvery.
func (r *run) onEvent(e model.Event) {
	if r.trace != nil {
		r.trace(Trace{RunID: r.ev.RunID, Event: &e})
	}
	var activity string
	switch e.Kind {
	case model.EventReasoningDelta:
		activity = "thinking"
	case model.EventTextDelta:
		activity = "writing report"
	case model.EventReclassify:
		r.update(func(ev *Event) {
			r.thinking, r.text = append(r.thinking, r.text...), r.text[:0]
			ev.Thinking, ev.Text = string(r.thinking), ""
		})
		return
	case model.EventToolCall:
		if e.ToolCall != nil {
			call := *e.ToolCall
			r.update(func(ev *Event) {
				r.thinking, r.text = r.thinking[:0], r.text[:0]
				ev.Activity, ev.Thinking, ev.Text = "", "", ""
				ev.Steps = append(ev.Steps, Step{ID: call.ID, Name: call.Name, Args: call.Args, Status: "running"})
			})
		}
		return
	default:
		return
	}
	r.mu.Lock()
	r.thinking = append(r.thinking, e.Reasoning...)
	r.text = append(r.text, e.Text...)
	emit := r.ev.Activity != activity || time.Since(r.lastEmit) >= textEvery
	r.mu.Unlock()
	if emit {
		r.update(func(ev *Event) {
			ev.Activity, ev.Thinking, ev.Text = activity, string(r.thinking), string(r.text)
		})
	}
}

// onToolResult records a finished step, its output cut to maxStepOutput.
func (r *run) onToolResult(call model.ToolCall, res tool.Result, err error) {
	if r.trace != nil {
		r.trace(Trace{RunID: r.ev.RunID, Result: &tool.CallResult{ToolCall: call, Result: res, Err: err}})
	}
	status, output := "ok", res.Content
	if err != nil {
		output = err.Error()
	}
	if err != nil || res.IsError {
		status = "failed"
	}
	if len(output) > maxStepOutput {
		output = textfmt.Prefix(output, maxStepOutput) + "\n… (truncated)"
	}
	r.update(func(ev *Event) {
		for i := range ev.Steps {
			if ev.Steps[i].ID == call.ID {
				ev.Steps[i].Status, ev.Steps[i].Output = status, output
			}
		}
	})
}

// onStats keeps the run's tokens and cost: billed where the endpoint says,
// else at the model's list price, marked as estimated.
func (r *run) onStats(s core.StepStats) {
	r.update(func(ev *Event) {
		ev.PromptTokens, ev.CompletionTokens = s.SessionPromptTokens, s.SessionCompletionTokens
		switch {
		case s.CostReported:
			ev.Cost += s.Cost
		case s.CostEstimate > 0:
			ev.Cost += s.CostEstimate
			ev.CostEstimated = true
		}
	})
}

// finish records the run's end, its report, and how long it took.
func (r *run) finish(report string, err error) {
	r.update(func(ev *Event) {
		ev.Status, ev.Report, ev.Err, ev.Activity = Done, report, err, ""
		if err == nil {
			ev.Text = report
		}
		if err != nil {
			ev.Status = Failed
		}
		if !ev.Started.IsZero() {
			ev.Took = time.Since(ev.Started)
		}
	})
}
