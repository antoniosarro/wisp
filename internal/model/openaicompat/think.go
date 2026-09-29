package openaicompat

import (
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
)

// The tags reasoning models such as DeepSeek R1 and Qwen3 wrap their
// thinking in, when the server leaves it in the content.
const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// thinkPhase is where a response stands relative to its think block.
type thinkPhase int

const (
	phaseStart    thinkPhase = iota // nothing but whitespace yet
	phaseUntagged                   // text with no opening tag; a bare </think> reclassifies it
	phaseThink                      // inside <think>
	phaseAnswer                     // after the think block: tags are literal text
)

// thinkFilter splits content deltas into Text/Reasoning events for backends
// that inline <think>...</think>. Only a tag opening the response counts,
// so an answer that mentions the tags stays text. A bare </think> on a
// line's start or end means the chat template opened the block in the
// prompt: the text before it was reasoning, reported with EventReclassify.
// One inside a line is a mention. Tags split across chunks are buffered.
type thinkFilter struct {
	phase   thinkPhase
	sent    bool   // untagged text was emitted
	midLine bool   // the untagged text so far doesn't end a line
	carry   string // a possible partial tag, held until the next chunk decides
}

// Feed splits the next content delta into events. Text that may start a
// tag is held back until a later Feed or Flush decides what it is.
func (f *thinkFilter) Feed(text string) []model.Event {
	var events []model.Event
	s := f.carry + text
	f.carry = ""

	for {
		switch f.phase {
		case phaseStart:
			trimmed := strings.TrimLeft(s, " \t\r\n")
			switch {
			case strings.HasPrefix(trimmed, thinkOpen):
				f.phase, s = phaseThink, trimmed[len(thinkOpen):]
				continue
			case strings.HasPrefix(thinkOpen, trimmed):
				f.carry = s // "" or a partial opening tag
				return events
			}
			f.phase = phaseUntagged
		case phaseUntagged:
			idx := strings.Index(s, thinkClose)
			if idx >= 0 {
				before, after := s[:idx], s[idx+len(thinkClose):]
				startsLine := before == "" && !f.midLine || strings.HasSuffix(before, "\n")
				if !startsLine && after == "" { // whether a line break follows decides
					events = f.untagged(events, before)
					f.carry = thinkClose
					return events
				}
				if !startsLine && after[0] != '\n' && after[0] != '\r' {
					// A mention inside a line, e.g. "closes with `</think>`".
					events = f.untagged(events, before+thinkClose)
					s = after
					continue
				}
				if f.sent {
					events = append(events, model.Event{Kind: model.EventReclassify})
				}
				events = appendText(events, model.EventReasoningDelta, s[:idx])
				f.phase, s = phaseAnswer, strings.TrimLeft(s[idx+len(thinkClose):], "\r\n")
				continue
			}
			return f.untagged(events, f.hold(s, thinkClose))
		case phaseThink:
			if idx := strings.Index(s, thinkClose); idx >= 0 {
				events = appendText(events, model.EventReasoningDelta, s[:idx])
				f.phase, s = phaseAnswer, s[idx+len(thinkClose):]
				continue
			}
			return appendText(events, model.EventReasoningDelta, f.hold(s, thinkClose))
		default:
			return appendText(events, model.EventTextDelta, s)
		}
	}
}

// untagged emits text of an untagged response, tracking whether it ends
// a line.
func (f *thinkFilter) untagged(events []model.Event, text string) []model.Event {
	if text != "" {
		f.sent, f.midLine = true, !strings.HasSuffix(text, "\n")
	}
	return appendText(events, model.EventTextDelta, text)
}

// hold buffers a suffix of s that may begin tag and returns the rest.
func (f *thinkFilter) hold(s, tag string) string {
	n := partialSuffixLen(s, tag)
	f.carry = s[len(s)-n:]
	return s[:len(s)-n]
}

// Flush emits buffered partial-tag text that never resolved, as reasoning
// inside a think block and as text elsewhere.
func (f *thinkFilter) Flush() []model.Event {
	kind := model.EventTextDelta
	if f.phase == phaseThink {
		kind = model.EventReasoningDelta
	}
	events := appendText(nil, kind, f.carry)
	f.carry = ""
	return events
}

// appendText appends text as an event of kind, putting it in the field
// that kind uses; empty text adds nothing.
func appendText(events []model.Event, kind model.EventKind, text string) []model.Event {
	if text == "" {
		return events
	}
	if kind == model.EventReasoningDelta {
		return append(events, model.Event{Kind: kind, Reasoning: text})
	}
	return append(events, model.Event{Kind: kind, Text: text})
}

// partialSuffixLen returns the length of the longest suffix of s that is a
// proper prefix of tag.
func partialSuffixLen(s, tag string) int {
	for n := min(len(tag)-1, len(s)); n > 0; n-- {
		if strings.HasSuffix(s, tag[:n]) {
			return n
		}
	}
	return 0
}
