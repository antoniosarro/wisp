package openaicompat

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
)

// chatChunk is one "data:" line of a streamed chat completion.
type chatChunk struct {
	Choices  []chatChunkChoice `json:"choices"`
	Usage    *usageWire        `json:"usage"`    // only on the final chunk
	Provider string            `json:"provider"` // OpenRouter: the upstream that served it
	// Error is a failure after the response started, e.g. vLLM's engine
	// errors or OpenRouter's upstream ones, often followed by [DONE].
	Error *struct {
		Message string          `json:"message"`
		Code    json.RawMessage `json:"code"` // a number or a string, by server
	} `json:"error"`
}

type usageWire struct {
	PromptTokens        int      `json:"prompt_tokens"`
	CompletionTokens    int      `json:"completion_tokens"`
	TotalTokens         int      `json:"total_tokens"`
	Cost                *float64 `json:"cost"` // OpenRouter: US dollars billed
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type chatChunkChoice struct {
	Delta        chatChunkDelta `json:"delta"`
	FinishReason string         `json:"finish_reason"`
}

type chatChunkDelta struct {
	Content          string              `json:"content"`
	ReasoningContent string              `json:"reasoning_content"`
	Reasoning        string              `json:"reasoning"` // OpenRouter's name for reasoning_content
	ToolCalls        []chatChunkToolCall `json:"tool_calls"`
}

// chatChunkToolCall is a fragment of a tool call: the first carries its id
// and name, later ones more of its arguments.
type chatChunkToolCall struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id"`
	Function chatToolCallFunction `json:"function"`
}

// maxLine caps an SSE line. A whole tool call can come in one line, e.g. a
// write of a big file; the buffer only grows that far when a line needs it.
const maxLine = 16 << 20

// readStream parses SSE lines from body, stopping early once emit returns
// false. It always ends with EventDone or EventError: a stream that stops
// without [DONE] is an error, since the answer may be cut short.
func readStream(body io.Reader, emit func(model.Event) bool) {
	st := streamState{slot: map[int]int{}}
	completed := false

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	for scanner.Scan() {
		// Lines without "data:" are SSE comments (OpenRouter's keep-alive
		// ": OPENROUTER PROCESSING") or event fields nobody here uses.
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		data = strings.TrimSpace(data)
		if !ok || data == "" {
			continue
		}
		if data == "[DONE]" {
			completed = true
			break
		}

		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			emit(errorEvent(fmt.Errorf("decoding chunk: %w", err)))
			return
		}
		if e := chunk.Error; e != nil {
			code, _ := strconv.Atoi(strings.Trim(string(e.Code), `"`))
			emit(errorEvent(&statusError{code: code, msg: "server error: " + cmp.Or(e.Message, string(e.Code), "unknown")}))
			return
		}
		if chunk.Usage != nil {
			st.usage = chunk.Usage.usage(chunk.Provider)
		}
		for _, choice := range chunk.Choices {
			events, done, err := st.apply(choice)
			completed = completed || done
			if !emitAll(emit, events) {
				return
			}
			if err != nil {
				emit(errorEvent(err))
				return
			}
		}
	}

	switch {
	case scanner.Err() != nil:
		emit(errorEvent(fmt.Errorf("reading stream: %w", scanner.Err())))
	case !completed:
		emit(errorEvent(fmt.Errorf("stream ended before completion: %w", io.ErrUnexpectedEOF)))
	default:
		emitAll(emit, st.finish())
	}
}

// usage converts the wire usage, served by provider.
func (u *usageWire) usage(provider string) *model.Usage {
	usage := &model.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		Provider:         provider,
	}
	if u.PromptTokensDetails != nil {
		usage.CachedTokens = u.PromptTokensDetails.CachedTokens
	}
	if u.Cost != nil {
		usage.Cost, usage.CostReported = *u.Cost, true
	}
	return usage
}

// streamState accumulates tool-call fragments and usage across chunks.
type streamState struct {
	calls     []*model.ToolCall // in the order they started
	slot      map[int]int       // wire index -> position in calls of its current call
	think     thinkFilter
	usage     *model.Usage
	truncated bool // finish_reason was "length"
}

// apply folds one choice delta into the state, returning the events it
// produced and whether it carried a finish_reason that ends the response
// normally. "length" counts: the partial answer is usable and the caller
// decides how to continue. Any other reason (e.g. "content_filter") is an
// error, since the answer was withheld.
func (s *streamState) apply(choice chatChunkChoice) (events []model.Event, done bool, err error) {
	switch choice.FinishReason {
	case "":
	case "stop", "tool_calls", "function_call":
		done = true
	case "length":
		done, s.truncated = true, true
	default:
		err = fmt.Errorf("response incomplete: finish_reason=%s", choice.FinishReason)
	}
	if choice.Delta.Content != "" {
		events = s.think.Feed(choice.Delta.Content)
	}
	if r := choice.Delta.ReasoningContent + choice.Delta.Reasoning; r != "" {
		events = append(events, model.Event{Kind: model.EventReasoningDelta, Reasoning: r})
	}
	for _, tc := range choice.Delta.ToolCalls {
		pos, seen := s.slot[tc.Index]
		// Servers that send each call whole may leave out index, so every
		// call arrives as index 0: a different id starts a new call.
		if seen && tc.ID != "" && s.calls[pos].ID != "" && s.calls[pos].ID != tc.ID {
			seen = false
		}
		if !seen {
			pos = len(s.calls)
			s.slot[tc.Index] = pos
			s.calls = append(s.calls, &model.ToolCall{})
		}
		call := s.calls[pos]
		if tc.ID != "" {
			call.ID = tc.ID
		}
		call.Name += tc.Function.Name
		call.Args = append(call.Args, tc.Function.Arguments...)
	}
	return events, done, err
}

// finish returns the trailing events: buffered text, tool calls, EventDone.
// A truncated response's tool calls are dropped: the last one's arguments
// were cut off mid-generation. Calls the backend sent without an id, or
// with one another call already has, get a new one, since results are
// matched to calls by id.
func (s *streamState) finish() []model.Event {
	events := s.think.Flush()
	if !s.truncated {
		seen := map[string]bool{}
		for _, call := range s.calls {
			if call.ID == "" || seen[call.ID] {
				call.ID = fmt.Sprintf("call_%016x", rand.Uint64())
			}
			seen[call.ID] = true
			events = append(events, model.Event{Kind: model.EventToolCall, ToolCall: call})
		}
	}
	return append(events, model.Event{Kind: model.EventDone, Usage: s.usage, Truncated: s.truncated})
}

// emitAll emits events in order, reporting false as soon as emit does.
func emitAll(emit func(model.Event) bool, events []model.Event) bool {
	for _, e := range events {
		if !emit(e) {
			return false
		}
	}
	return true
}

func errorEvent(err error) model.Event {
	return model.Event{Kind: model.EventError, Err: err}
}
