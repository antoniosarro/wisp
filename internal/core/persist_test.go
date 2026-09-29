package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/span"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

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

func TestLoopPersistenceErrorAbortsTurn(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "hi"}, {Kind: model.EventDone}}}}
	l := &Loop{Provider: p, Tools: tool.NewRegistry(), Store: &memStore{fail: errors.New("disk full")}}
	if _, err := l.Run(context.Background(), "hello"); err == nil {
		t.Fatal("expected an error when persistence fails")
	}
	if p.Calls != 0 {
		t.Errorf("provider was called %d times, want 0: the turn stops before streaming", p.Calls)
	}
}

// failingAt fails the append of the n-th message.
type failingAt struct {
	memStore
	n int
}

func (s *failingAt) AppendMessage(id string, msg model.Message) error {
	if len(s.msgs) == s.n {
		return errors.New("disk full")
	}
	return s.memStore.AppendMessage(id, msg)
}

func TestLoopPersistenceErrorMidTurnAbortsAfterToolResult(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "call_1", Name: "echo", Args: json.RawMessage(`{}`)}}, {Kind: model.EventDone}},
	}}
	// user (0), assistant (1): the tool result (2) fails
	l := &Loop{Provider: p, Tools: tool.NewRegistry(testutil.EchoTool{}), Store: &failingAt{n: 2}}
	if _, err := l.Run(context.Background(), "go"); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Run = %v, want the store's error", err)
	}
}

// WithSession keeps the configuration and starts the session's own state
// afresh.
func TestWithSession(t *testing.T) {
	store := &memStore{}
	history := []model.Message{{Role: model.RoleUser, Content: "resumed"}}
	l := &Loop{System: "sys", ContextWindow: 8192, AutoCompact: true, SessionID: "old",
		Compacted: &Compaction{FirstKept: 3}, Compactions: []Compaction{{FirstKept: 3}}}
	l.stats.RequestCount = 7

	rec := span.NewRecorder(&spanSink{spans: map[string]span.Record{}}, "new")
	defer rec.Close()
	next := l.WithSession(store, "new", history, rec)
	if next.System != "sys" || next.ContextWindow != 8192 || !next.AutoCompact {
		t.Errorf("configuration lost: %+v", next)
	}
	if next.Store != store || next.SessionID != "new" || !reflect.DeepEqual(next.History, history) || next.Spans != rec {
		t.Errorf("session not switched: store %v, id %q", next.Store, next.SessionID)
	}
	if next.Compacted != nil || next.Compactions != nil || next.stats.RequestCount != 0 {
		t.Errorf("the old session's state carried over: compacted %v, stats %+v", next.Compacted, next.stats)
	}
	if l.SessionID != "old" || l.Compacted == nil {
		t.Error("WithSession changed the original loop")
	}
}
