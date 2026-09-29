package openaicompat

import (
	"errors"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func TestTextDeltas(t *testing.T) {
	got := parse(`data: {"choices":[{"delta":{"content":"hel"}}]}
data:{"choices":[{"delta":{"content":"lo"}}]}
: OPENROUTER PROCESSING
data: [DONE]
`)
	want := []model.Event{{Kind: model.EventTextDelta, Text: "hel"}, {Kind: model.EventTextDelta, Text: "lo"}, {Kind: model.EventDone}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Text != want[i].Text {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReasoningFields(t *testing.T) {
	got := parse(`data: {"choices":[{"delta":{"reasoning_content":"vllm "}}]}
data: {"choices":[{"delta":{"reasoning":"openrouter"}}]}
data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}
data: [DONE]
`)
	var reasoning string
	for _, e := range got {
		reasoning += e.Reasoning
	}
	if reasoning != "vllm openrouter" {
		t.Errorf("reasoning = %q, want both fields' text", reasoning)
	}
}

func TestToolCallAccumulation(t *testing.T) {
	got := toolCalls(parse(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read","arguments":""}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}}]}}]}
data: [DONE]
`))
	if len(got) != 1 || got[0].ID != "call_1" || got[0].Name != "read" || string(got[0].Args) != `{"path":"a.go"}` {
		t.Fatalf("calls = %+v", got)
	}
}

func TestToolCallsWithoutIndex(t *testing.T) {
	got := toolCalls(parse(`data: {"choices":[{"delta":{"tool_calls":[{"id":"a","function":{"name":"read","arguments":"{\"path\":\"x\"}"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"id":"b","function":{"name":"ls","arguments":"{}"}}]}}]}
data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}
data: [DONE]
`))
	if len(got) != 2 || got[0].Name != "read" || got[1].Name != "ls" || string(got[1].Args) != "{}" {
		t.Fatalf("calls = %+v", got)
	}
}

func TestInterleavedToolCalls(t *testing.T) {
	got := toolCalls(parse(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"read","arguments":"{\"pa"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"ls","arguments":"{"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":1}"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"}"}}]}}]}
data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}
data: [DONE]
`))
	if len(got) != 2 || string(got[0].Args) != `{"path":1}` || string(got[1].Args) != "{}" {
		t.Fatalf("calls = %+v", got)
	}
}

func TestToolCallIDsAreMadeUnique(t *testing.T) {
	got := toolCalls(parse(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","function":{"name":"read","arguments":"{}"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"x","function":{"name":"ls","arguments":"{}"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":2,"function":{"name":"glob","arguments":"{}"}}]}}]}
data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}
data: [DONE]
`))
	if len(got) != 3 || got[0].ID != "x" || got[1].ID == "x" || got[1].ID == "" || got[2].ID == "" {
		t.Fatalf("calls = %+v; want the duplicate renamed and the missing id filled", got)
	}
}

func TestLongLine(t *testing.T) {
	big := strings.Repeat("x", 2<<20)
	got := toolCalls(parse(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"write","arguments":"{\"content\":\"` + big + `\"}"}}]}}]}
data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}
data: [DONE]
`))
	if len(got) != 1 || len(got[0].Args) < 2<<20 {
		t.Fatalf("a 2 MB line lost its call: %d calls", len(got))
	}
}

func TestUsageAttachedToDone(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       model.Usage
	}{
		{
			"cached tokens",
			`data: {"choices":[{"delta":{"content":"hi"}}]}
data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":8}}}
data: [DONE]
`,
			model.Usage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15, CachedTokens: 8},
		},
		{
			"OpenRouter's billed cost and provider",
			`data: {"provider":"DeepInfra","choices":[{"delta":{"content":"hi"}}]}
data: {"provider":"DeepInfra","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"cost":0.00042}}
data: [DONE]
`,
			model.Usage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15, Cost: 0.00042, CostReported: true, Provider: "DeepInfra"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := parse(c.body)
			done := got[len(got)-1]
			if done.Kind != model.EventDone || done.Usage == nil || *done.Usage != c.want {
				t.Errorf("last event = %+v, usage %+v; want Done with %+v", done, done.Usage, c.want)
			}
		})
	}
}

func TestErrorChunk(t *testing.T) {
	for _, c := range []struct {
		name, chunk, wantMsg string
		overflow             bool
	}{
		{"engine error", `{"error":{"message":"engine died","code":500}}`, "engine died", false},
		{"numeric overflow code", `{"error":{"message":"This model's maximum context length is 8192 tokens","code":400}}`, "maximum context", true},
		{"quoted overflow code", `{"error":{"message":"context length exceeded","code":"400"}}`, "context length", true},
		{"no message", `{"error":{"code":"upstream_error"}}`, "upstream_error", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := parse(`data: {"choices":[{"delta":{"content":"part"}}]}` + "\ndata: " + c.chunk + "\ndata: [DONE]\n")
			last := got[len(got)-1]
			if last.Kind != model.EventError || !strings.Contains(last.Err.Error(), c.wantMsg) {
				t.Fatalf("error chunk ended as %+v, want an error mentioning %q", last, c.wantMsg)
			}
			if errors.Is(last.Err, model.ErrContextOverflow) != c.overflow {
				t.Errorf("overflow = %v, want %v", !c.overflow, c.overflow)
			}
		})
	}
}

// A stream that stops early, or whose answer was withheld, must not look
// like a complete answer.
func TestIncompleteResponses(t *testing.T) {
	for name, body := range map[string]string{
		"no [DONE]":      "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n",
		"content filter": "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"content_filter\"}]}\ndata: [DONE]\n",
		"malformed":      "data: {not json}\n",
	} {
		if got := parse(body); len(got) == 0 || got[len(got)-1].Kind != model.EventError {
			t.Errorf("%s: reported success: %+v", name, got)
		}
	}
}

func TestLengthTruncation(t *testing.T) {
	got := parse(`data: {"choices":[{"delta":{"content":"partial answer"}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read","arguments":"{\"pa"}}]}}]}
data: {"choices":[{"delta":{},"finish_reason":"length"}]}
data: [DONE]
`)
	var text string
	for _, e := range got {
		switch e.Kind {
		case model.EventTextDelta:
			text += e.Text
		case model.EventToolCall:
			t.Errorf("truncated response emitted tool call %+v; its arguments are cut off", e.ToolCall)
		case model.EventError:
			t.Fatalf("truncated response reported an error: %v", e.Err)
		}
	}
	if text != "partial answer" {
		t.Errorf("text = %q, want the partial answer kept", text)
	}
	if last := got[len(got)-1]; last.Kind != model.EventDone || !last.Truncated {
		t.Errorf("last event = %+v, want Done with Truncated", last)
	}
}
