package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

func TestLoopPlainAnswerNoTools(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{
			{Kind: model.EventTextDelta, Text: "hel"},
			{Kind: model.EventTextDelta, Text: "lo"},
			{Kind: model.EventDone},
		},
	}}
	l := &Loop{Provider: p, Tools: tool.NewRegistry()}

	got, err := l.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "hello" {
		t.Errorf("answer = %q, want %q", got, "hello")
	}
	if p.Calls != 1 {
		t.Errorf("provider called %d times, want 1", p.Calls)
	}

	if len(l.History) != 2 {
		t.Fatalf("History len = %d, want 2 (user + assistant)", len(l.History))
	}
	if l.History[0].Role != model.RoleUser || l.History[0].Content != "hi" {
		t.Errorf("History[0] = %+v", l.History[0])
	}
	if l.History[1].Role != model.RoleAssistant || l.History[1].Content != "hello" {
		t.Errorf("History[1] = %+v", l.History[1])
	}
}

func TestLoopDispatchesToolCallAndContinues(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{
			{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "call_1", Name: "echo", Args: json.RawMessage(`{"x":1}`)}},
			{Kind: model.EventDone},
		},
		{
			{Kind: model.EventTextDelta, Text: "done"},
			{Kind: model.EventDone},
		},
	}}
	l := &Loop{Provider: p, Tools: tool.NewRegistry(testutil.EchoTool{})}

	got, err := l.Run(context.Background(), "run echo")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "done" {
		t.Errorf("answer = %q, want %q", got, "done")
	}
	if p.Calls != 2 {
		t.Fatalf("provider called %d times, want 2", p.Calls)
	}

	// user, assistant(tool_call), tool(result), assistant(final)
	if len(l.History) != 4 {
		t.Fatalf("History len = %d, want 4: %+v", len(l.History), l.History)
	}
	toolMsg := l.History[2]
	if toolMsg.Role != model.RoleTool || toolMsg.ToolCallID != "call_1" {
		t.Errorf("History[2] = %+v, want the tool result linked to call_1", toolMsg)
	}
	if toolMsg.Content != `echoed: {"x":1}` {
		t.Errorf("tool result content = %q", toolMsg.Content)
	}

	// the second provider call should have seen the tool result in context
	sawToolMsg := false
	for _, m := range p.LastReq.Messages {
		if m.Role == model.RoleTool && m.ToolCallID == "call_1" {
			sawToolMsg = true
		}
	}
	if !sawToolMsg {
		t.Error("second Stream() call did not receive the tool result in its Messages")
	}
}

func TestLoopUnknownToolResultFeedsBackAsError(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{
			{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "call_1", Name: "nope"}},
			{Kind: model.EventDone},
		},
		{
			{Kind: model.EventTextDelta, Text: "ok"},
			{Kind: model.EventDone},
		},
	}}
	l := &Loop{Provider: p, Tools: tool.NewRegistry()}

	_, err := l.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	toolMsg := l.History[2]
	if toolMsg.Role != model.RoleTool || toolMsg.Content == "" {
		t.Errorf("History[2] = %+v, want an error message fed back as tool content", toolMsg)
	}
}

func TestLoopStreamError(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventError, Err: errors.New("boom")}},
	}}
	l := &Loop{Provider: p, Tools: tool.NewRegistry()}

	_, err := l.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected an error to propagate from the stream")
	}
}

func TestLoopMaxIterations(t *testing.T) {
	// A provider that always requests the same tool call, never finishing.
	loopingTurn := []model.Event{
		{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c", Name: "echo", Args: json.RawMessage(`{}`)}},
		{Kind: model.EventDone},
	}
	turns := make([][]model.Event, 5)
	for i := range turns {
		turns[i] = loopingTurn
	}
	p := &testutil.ScriptedProvider{Turns: turns}
	l := &Loop{Provider: p, Tools: tool.NewRegistry(testutil.EchoTool{}), MaxIterations: 3}

	_, err := l.Run(context.Background(), "go")
	if err == nil {
		t.Fatal("expected an error when MaxIterations is exceeded")
	}
	if p.Calls != 4 {
		t.Errorf("provider called %d times, want MaxIterations (3) plus one wrap-up request", p.Calls)
	}
}

func TestLoopWrapUpAfterMaxIterations(t *testing.T) {
	looping := []model.Event{
		{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c", Name: "echo", Args: json.RawMessage(`{}`)}},
		{Kind: model.EventDone},
	}
	summary := []model.Event{{Kind: model.EventTextDelta, Text: "did half"}, {Kind: model.EventDone}}
	var choices []string
	p := &recordingProvider{inner: &testutil.ScriptedProvider{Turns: [][]model.Event{looping, looping, summary}}, choices: &choices}
	l := &Loop{Provider: p, Tools: tool.NewRegistry(testutil.EchoTool{}), MaxIterations: 2}

	answer, err := l.Run(context.Background(), "go")
	if err != nil || answer != "did half" {
		t.Fatalf("Run = %q, %v; want the wrap-up summary", answer, err)
	}
	if choices[2] != "none" {
		t.Errorf("wrap-up tool choice = %q, want none", choices[2])
	}
	if last := l.History[len(l.History)-1]; last.Role != model.RoleAssistant || last.Content != "did half" {
		t.Errorf("last message = %+v, want the summary", last)
	}
}

func TestLoopRepairsInterruptedHistory(t *testing.T) {
	answer := []model.Event{{Kind: model.EventTextDelta, Text: "ok"}, {Kind: model.EventDone}}
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{answer}}
	l := &Loop{Provider: p, History: []model.Message{
		{Role: model.RoleUser, Content: "a"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "1", Name: "echo"}, {ID: "2", Name: "echo"}}},
		{Role: model.RoleTool, ToolCallID: "1", Content: "done"},
	}}
	if _, err := l.Run(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	repaired := l.History[3]
	if repaired.Role != model.RoleTool || repaired.ToolCallID != "2" || !repaired.IsError {
		t.Fatalf("history[3] = %+v, want a placeholder result for call 2", repaired)
	}

	l.History = append(l.History, model.Message{Role: model.RoleUser, Content: "lost"})
	p.Turns = append(p.Turns, answer)
	if _, err := l.Run(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	if got := l.History[7]; got.Role != model.RoleAssistant || got.Content != InterruptedReply {
		t.Fatalf("history[7] = %+v, want a placeholder answer after the unanswered prompt", got)
	}
}

func TestLoopOnEventCallback(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{
			{Kind: model.EventTextDelta, Text: "hi"},
			{Kind: model.EventDone},
		},
	}}
	var seen []model.EventKind
	l := &Loop{Provider: p, Tools: tool.NewRegistry(), OnEvent: func(e model.Event) {
		seen = append(seen, e.Kind)
	}}

	if _, err := l.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen) != 2 || seen[0] != model.EventTextDelta || seen[1] != model.EventDone {
		t.Errorf("seen = %v, want [TextDelta, Done]", seen)
	}
}

func TestLoopSendsSystemPromptWithoutPersistingIt(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "ok"}, {Kind: model.EventDone}}}}
	l := &Loop{Provider: p, System: "be brief"}
	if _, err := l.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if msgs := p.LastReq.Messages; len(msgs) != 2 || msgs[0].Role != model.RoleSystem || msgs[0].Content != "be brief" {
		t.Fatalf("request messages = %+v, want the system prompt first", msgs)
	}
	for _, m := range l.History {
		if m.Role == model.RoleSystem {
			t.Fatal("system prompt was appended to History")
		}
	}
}

func TestFinishCheckRemindsOncePerTurn(t *testing.T) {
	answer := []model.Event{{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventDone}}
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{answer, answer, answer}}
	checks := 0
	l := &Loop{Provider: p, FinishCheck: func(turn []model.Message) string {
		checks++
		if turn[0].Content != "second" {
			t.Errorf("FinishCheck saw %q, want only the current turn", turn[0].Content)
		}
		return "finish your tasks"
	}}
	l.History = []model.Message{{Role: model.RoleUser, Content: "first"}, {Role: model.RoleAssistant, Content: "ok"}}

	if _, err := l.Run(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if p.Calls != 2 || checks != 1 {
		t.Fatalf("provider calls = %d, checks = %d; want one reminder then a final answer", p.Calls, checks)
	}
	if p.LastReq.ToolChoice != "" {
		t.Fatalf("ToolChoice = %q without tools, want unset", p.LastReq.ToolChoice)
	}
	reminder := l.History[4]
	if reminder.Role != model.RoleUser || reminder.Content != ReminderPrefix+"finish your tasks" {
		t.Fatalf("reminder message = %+v", reminder)
	}
}

func TestReminderStepDoesNotForceToolCall(t *testing.T) {
	answer := []model.Event{{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventDone}}
	var choices []string
	p := &recordingProvider{inner: &testutil.ScriptedProvider{Turns: [][]model.Event{answer, answer}}, choices: &choices}
	l := &Loop{Provider: p, Tools: tool.NewRegistry(testutil.EchoTool{}), FinishCheck: func([]model.Message) string { return "update your tasks" }}
	if _, err := l.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(choices) != 2 || choices[0] != "" || choices[1] != "" {
		t.Fatalf("tool choices = %q, want the model free to answer or ask after the reminder", choices)
	}
}

type recordingProvider struct {
	inner   model.Provider
	choices *[]string
}

func (p *recordingProvider) Stream(ctx context.Context, req model.Request) (<-chan model.Event, error) {
	*p.choices = append(*p.choices, req.ToolChoice)
	return p.inner.Stream(ctx, req)
}

func TestLoopContinuesAfterTruncatedResponse(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventTextDelta, Text: "thinking forever"}, {Kind: model.EventDone, Truncated: true}},
		{{Kind: model.EventTextDelta, Text: "e2e4"}, {Kind: model.EventDone}},
	}}
	l := &Loop{Provider: p}

	got, err := l.Run(context.Background(), "best move?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "e2e4" {
		t.Errorf("answer = %q, want the response after the truncated one", got)
	}
	// user, truncated assistant, reminder, final assistant
	if len(l.History) != 4 || l.History[2].Role != model.RoleUser ||
		!strings.HasPrefix(l.History[2].Content, ReminderPrefix) {
		t.Errorf("history = %+v, want a reminder after the truncated response", l.History)
	}
}

// countingTool counts its runs; risky makes it a write.
type countingTool struct {
	name  string
	risky bool
	runs  *int
}

func (c countingTool) Schema() model.ToolSchema { return model.ToolSchema{Name: c.name} }
func (c countingTool) Risky() bool              { return c.risky }
func (c countingTool) Run(_ context.Context, args json.RawMessage) (tool.Result, error) {
	*c.runs++
	return tool.Result{Content: c.name + " " + string(args)}, nil
}

func TestRepeatedReadOnlyCallsDontRunAgain(t *testing.T) {
	calls := func(cs ...model.ToolCall) []model.Event {
		var events []model.Event
		for i := range cs {
			events = append(events, model.Event{Kind: model.EventToolCall, ToolCall: &cs[i]})
		}
		return append(events, model.Event{Kind: model.EventDone})
	}
	read := func(id, args string) model.ToolCall {
		return model.ToolCall{ID: id, Name: "read", Args: json.RawMessage(args)}
	}
	answer := []model.Event{{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventDone}}
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		calls(read("1", `{"path":"a","n":1}`)),
		calls(read("2", `{ "n": 1, "path": "a" }`)), // same call, other formatting: skipped
		calls(model.ToolCall{ID: "3", Name: "write", Args: json.RawMessage(`{}`)}),
		calls(read("4", `{"path":"a","n":1}`)),                                                       // after a write: runs again
		calls(read("5", `{"path":"b"}`), read("6", `{"path":"b"}`), read("7", `{"path":"a","n":1}`)), // in one batch: once
		answer,
		// next turn
		calls(read("8", `{"path":"a","n":1}`)), // a new turn starts fresh
		answer,
	}}
	var reads, writes int
	var reported []string
	l := &Loop{
		Provider: p,
		Tools:    tool.NewRegistry(countingTool{name: "read", runs: &reads}, countingTool{name: "write", risky: true, runs: &writes}),
		OnToolResult: func(call model.ToolCall, _ tool.Result, _ error) {
			reported = append(reported, call.ID)
		},
	}
	if _, err := l.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if reads != 3 || writes != 1 {
		t.Errorf("read ran %d times, write %d; want 3 and 1", reads, writes)
	}
	var skipped []string
	var order []string
	for _, m := range l.History {
		if m.Role != model.RoleTool {
			continue
		}
		order = append(order, m.ToolCallID)
		if m.Content == RepeatedCallContent {
			if !m.IsError {
				t.Errorf("skipped call %s should be marked as an error", m.ToolCallID)
			}
			skipped = append(skipped, m.ToolCallID)
		}
	}
	if got := strings.Join(skipped, " "); got != "2 6 7" {
		t.Errorf("skipped %q, want 2 6 7", got)
	}
	if got := strings.Join(order, " "); got != "1 2 3 4 5 6 7" {
		t.Errorf("results in order %q, want request order", got)
	}
	slices.Sort(reported)
	if got := strings.Join(reported, " "); got != "1 2 3 4 5 6 7" {
		t.Errorf("OnToolResult saw %q, want every call, skipped ones included", got)
	}

	if _, err := l.Run(context.Background(), "again"); err != nil {
		t.Fatal(err)
	}
	if reads != 4 {
		t.Errorf("read ran %d times after a new turn, want 4", reads)
	}
}

// memStore records what the loop persists.
type memStore struct {
	ids  []string
	msgs []model.Message
	fail error
}

func (s *memStore) AppendMessage(id string, msg model.Message) error {
	if s.fail != nil {
		return s.fail
	}
	s.ids, s.msgs = append(s.ids, id), append(s.msgs, msg)
	return nil
}

func TestLoopPersistsEveryMessage(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c", Name: "echo", Args: json.RawMessage(`{}`)}}, {Kind: model.EventDone}},
		{{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventDone}},
	}}
	store := &memStore{}
	l := &Loop{Provider: p, Tools: tool.NewRegistry(testutil.EchoTool{}), System: "be brief", Store: store, SessionID: "s1"}
	if _, err := l.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	// user, assistant with the call, its result, the answer; never the system prompt
	if len(store.msgs) != 4 || !reflect.DeepEqual(store.msgs, l.History) || store.ids[0] != "s1" {
		t.Errorf("persisted %+v under %v, want History under s1", store.msgs, store.ids)
	}

	store.fail = errors.New("disk full")
	if _, err := l.Run(context.Background(), "again"); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("Run with a failing store = %v, want the store's error", err)
	}
}

func TestLoopStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "late"}, {Kind: model.EventDone}}}}
	l := &Loop{Provider: p}
	if _, err := l.Run(ctx, "hi"); !errors.Is(err, context.Canceled) || p.Calls != 0 {
		t.Errorf("Run = %v after %d requests, want context.Canceled before any", err, p.Calls)
	}
}

func TestReclassifiedTextIsNotTheAnswer(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{
		{Kind: model.EventTextDelta, Text: "let me think"},
		{Kind: model.EventReclassify},
		{Kind: model.EventTextDelta, Text: "42"},
		{Kind: model.EventDone},
	}}}
	if got, err := (&Loop{Provider: p}).Run(context.Background(), "q"); err != nil || got != "42" {
		t.Errorf("Run = %q, %v; want only the text after the reclassified part", got, err)
	}
}

func TestLoopDropsImagesFromEarlierTurns(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "ok"}, {Kind: model.EventDone}}}}
	l := &Loop{Provider: p, History: []model.Message{
		{Role: model.RoleUser, Content: "look"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "1", Name: "read"}}},
		{Role: model.RoleTool, ToolCallID: "1", Content: "a.png", Images: []model.Image{{MIME: "image/png", Data: []byte{1}}}},
		{Role: model.RoleAssistant, Content: "a cat"},
	}}
	if _, err := l.Run(context.Background(), "and now?"); err != nil {
		t.Fatal(err)
	}
	if got := l.History[2]; got.Images != nil || !strings.Contains(got.Content, "no longer attached") {
		t.Errorf("earlier tool result = %+v, want its image dropped with a note", got)
	}
}
