package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/textfmt"
	"github.com/antoniosarro/wisp/internal/tokencount"
)

// Observation masking: old tool output, and the bodies of old write and
// edit calls, are replaced by stand-ins that say what they were and how to
// get them back (stub.go). History keeps the originals; requests send the
// stand-ins (sent).

// Masking thresholds and sizes. The percentages are shares of the history
// budget (budget.go).
const (
	maskTriggerPct = 60 // mask old tool output past this share of the budget
	maskTargetPct  = 40 // and down to this share, in one batch

	// minElideBytes is the smallest tool output worth masking: shorter
	// output costs little more than its stand-in.
	minElideBytes = 500
	// minArgBytes is the smallest string argument of a write or edit call
	// worth masking.
	minArgBytes = 200

	protectedSteps = 10 // recent steps whose output is never masked...
	protectedPct   = 30 // ...unless they fill more than this share of the budget
)

// Masking classes, masked in this order: output a later call made stale
// costs nothing to lose, and a file's written body is on disk.
const (
	classSuperseded = iota
	classFileBody
	classOther
)

// fileWriters change the file their path argument names.
var fileWriters = map[string]bool{"write": true, "edit": true, "multi_edit": true}

// Elider is implemented by stores that remember masked tool output, so a
// resumed session doesn't send it in full again.
type Elider interface {
	// ElideMessage records the stand-in for the index-th message of the
	// session, a tool result answering toolCallID.
	ElideMessage(sessionID string, index int, toolCallID, stub string) error
	// ElideToolCalls records the tool calls of the index-th message, an
	// assistant message, with their elided arguments.
	ElideToolCalls(sessionID string, index int, calls []model.ToolCall) error
}

// masking is one planned change: a tool result's stand-in (call < 0), or
// the elided arguments of the call-th tool call of an assistant message.
type masking struct {
	msg, call int
	stub      string
	saved     float64 // estimated tokens freed
}

// mask replaces old tool output, and old write and edit bodies, with
// stand-ins until History's calibrated size is at most target tokens (a
// target of 0 masks everything it can). Output a later call superseded
// goes first, then file bodies, then the rest, oldest first within each.
// Results after the latest assistant message are never masked: the model
// hasn't acted on them yet. With protect, neither are the last
// protectedSteps steps, up to protectedPct of the budget. The batch is
// applied only if it frees at least minFree tokens, so the backend's
// prefix cache is thrown away only for a worthwhile gain. The stand-ins
// are stored, when the store supports it, so a resumed session doesn't
// send the output in full again. It reports whether anything changed.
func (l *Loop) mask(target int, protect bool, minFree int) bool {
	end := l.maskableEnd(protect)
	calls := map[string]model.ToolCall{}
	for _, msg := range l.History {
		for _, call := range msg.ToolCalls {
			calls[call.ID] = call
		}
	}
	stale := superseded(l.History)
	latestTodo := -1 // the plan in force stays readable
	for i, msg := range l.History {
		if msg.Role == model.RoleTool && calls[msg.ToolCallID].Name == "todo" {
			latestTodo = i
		}
	}

	total := float64(l.projectedHistory())
	var plan []masking
	add := func(m masking) {
		m.saved *= l.ratio()
		plan = append(plan, m)
		total -= m.saved
	}
	for class := classSuperseded; class <= classOther; class++ {
		for i := l.keptFrom(); i < end && total > float64(target); i++ {
			msg := l.History[i]
			if class == classFileBody {
				for j, call := range msg.ToolCalls {
					if !fileWriters[call.Name] || call.Elided != nil {
						continue
					}
					if args, ok := maskArgs(call.Args); ok {
						add(masking{msg: i, call: j, stub: string(args), saved: tokens(call.Args) - tokens(args)})
					}
				}
				continue
			}
			if msg.Role != model.RoleTool || msg.Elided != "" || len(msg.Content) < minElideBytes || i == latestTodo {
				continue
			}
			if stale[msg.ToolCallID] != (class == classSuperseded) {
				continue
			}
			call := calls[msg.ToolCallID]
			stub := maskStub(call, msg, spillPath(call, msg.Content), stale[msg.ToolCallID])
			add(masking{msg: i, call: -1, stub: stub, saved: tokens(msg.Content) - tokens(stub)})
		}
	}

	freed := 0.0
	for _, m := range plan {
		freed += m.saved
	}
	if len(plan) == 0 || freed < float64(minFree) {
		return false
	}
	elider, _ := l.Store.(Elider)
	for _, m := range plan {
		msg := &l.History[m.msg]
		if m.call >= 0 {
			msg.ToolCalls[m.call].Elided = json.RawMessage(m.stub)
			if elider != nil {
				// Losing the record only means masking again after a resume.
				_ = elider.ElideToolCalls(l.SessionID, m.msg, msg.ToolCalls)
			}
			continue
		}
		msg.Elided = m.stub
		writeSpill(spillFrom(m.stub), msg.Content)
		if elider != nil {
			_ = elider.ElideMessage(l.SessionID, m.msg, msg.ToolCallID, m.stub)
		}
	}
	l.tokens.counted = 0 // earlier messages changed: count again
	return true
}

// tokens is the local estimate of s, as a float for the plan's arithmetic.
func tokens[T string | json.RawMessage](s T) float64 {
	return float64(tokencount.Count(string(s)))
}

// maskableEnd is the index of the first message mask must leave alone.
func (l *Loop) maskableEnd(protect bool) int {
	last := len(l.History) - 1
	for last >= 0 && l.History[last].Role != model.RoleAssistant {
		last--
	}
	if !protect || last < 0 {
		return max(last, 0)
	}
	limit := float64(l.historyBudget() * protectedPct / 100)
	from, steps, used := last, 0, 0.0
	for i := len(l.History) - 1; i >= 0; i-- {
		used += float64(messageTokens(l.History[i])) * l.ratio()
		if limit > 0 && used > limit {
			break
		}
		if l.History[i].Role == model.RoleAssistant {
			from = min(from, i)
			if steps++; steps == protectedSteps {
				break
			}
		}
	}
	return from
}

// superseded returns the ids of calls whose output a later call made
// stale: the same call run again, a read of a file later changed (by a
// call that succeeded) or read again over the same lines, and every todo
// list but the latest.
func superseded(history []model.Message) map[string]bool {
	failed := map[string]bool{}
	for _, msg := range history {
		if msg.Role == model.RoleTool && msg.IsError {
			failed[msg.ToolCallID] = true
		}
	}
	stale := map[string]bool{}
	ran := map[string]bool{}
	changed := map[string]bool{}
	reads := map[string][][2]int{}
	todo := false
	// Newest first: what a call supersedes comes before it.
	for i := len(history) - 1; i >= 0; i-- {
		calls := history[i].ToolCalls
		for j := len(calls) - 1; j >= 0; j-- {
			call := calls[j]
			key := callKey(call)
			path, span := readSpan(call)
			switch {
			case ran[key], call.Name == "todo" && todo:
				stale[call.ID] = true
			case call.Name == "read" && changed[path]:
				stale[call.ID] = true
			case call.Name == "read":
				for _, later := range reads[path] {
					if later[0] <= span[0] && later[1] >= span[1] {
						stale[call.ID] = true
					}
				}
			}
			ran[key] = true
			switch {
			case call.Name == "todo":
				todo = true
			case call.Name == "read":
				reads[path] = append(reads[path], span)
			case fileWriters[call.Name] && !failed[call.ID]:
				changed[path] = true
			}
		}
	}
	return stale
}

// defaultReadLines is how many lines read returns without a limit.
const defaultReadLines = 2000

// readSpan returns the cleaned path a call names and, for a read, the
// lines it covers.
func readSpan(call model.ToolCall) (string, [2]int) {
	var a struct {
		Path          string
		Offset, Limit int
	}
	_ = json.Unmarshal(call.Args, &a)
	if a.Limit <= 0 {
		a.Limit = defaultReadLines
	}
	return filepath.Clean(a.Path), [2]int{a.Offset, a.Offset + a.Limit}
}

// maskArgs replaces the long strings in a write or edit call's arguments
// with a stand-in, reporting whether any were long enough to bother. The
// path stays: it says which file to read for the current content.
func maskArgs(args json.RawMessage) (json.RawMessage, bool) {
	if len(args) < minElideBytes {
		return nil, false
	}
	var v any
	if json.Unmarshal(args, &v) != nil {
		return nil, false
	}
	masked := false
	var walk func(any) any
	walk = func(v any) any {
		switch v := v.(type) {
		case string:
			if len(v) < minArgBytes {
				return v
			}
			masked = true
			return fmt.Sprintf("<masked: %d lines, %s; read the file for its current content>", lineCount(v), textfmt.Size(int64(len(v))))
		case map[string]any:
			for k, e := range v {
				if k != "path" {
					v[k] = walk(e)
				}
			}
		case []any:
			for i, e := range v {
				v[i] = walk(e)
			}
		}
		return v
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // the model reads these
	err := enc.Encode(walk(v))
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), masked && err == nil
}

// sent is msg as a request carries it: masked output and arguments are
// replaced by their stand-ins.
func sent(msg model.Message) model.Message {
	if msg.Elided != "" {
		msg.Content, msg.Images = msg.Elided, nil
	}
	if slices.ContainsFunc(msg.ToolCalls, func(c model.ToolCall) bool { return c.Elided != nil }) {
		msg.ToolCalls = slices.Clone(msg.ToolCalls) // History keeps the originals
		for i, c := range msg.ToolCalls {
			if c.Elided != nil {
				msg.ToolCalls[i].Args, msg.ToolCalls[i].Elided = c.Elided, nil
			}
		}
	}
	return msg
}
