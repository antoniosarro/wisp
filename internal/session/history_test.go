package session

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateSession(t *testing.T) {
	s := openTestStore(t)

	id, err := s.CreateSession("qwen2.5-coder")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id == "" {
		t.Fatal("CreateSession returned an empty id")
	}

	id2, err := s.CreateSession("qwen2.5-coder")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id2 == id {
		t.Error("two CreateSession calls returned the same id")
	}
}

func TestAppendAndLoadHistoryRoundTrip(t *testing.T) {
	s := openTestStore(t)
	sid, err := s.CreateSession("m")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	want := []model.Message{
		{Role: model.RoleUser, Content: "read go.mod"},
		{
			Role:    model.RoleAssistant,
			Content: "",
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "read", Args: json.RawMessage(`{"path":"go.mod"}`)},
			},
		},
		{Role: model.RoleTool, Content: "module wisp", ToolCallID: "call_1"},
		{Role: model.RoleAssistant, Content: "the module is wisp"},
	}

	for _, m := range want {
		if err := s.AppendMessage(sid, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	got, err := s.LoadHistory(sid)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content || got[i].ToolCallID != want[i].ToolCallID {
			t.Errorf("message %d = %+v, want %+v", i, got[i], want[i])
			continue
		}
		if !reflect.DeepEqual(got[i].ToolCalls, want[i].ToolCalls) {
			t.Errorf("message %d ToolCalls = %+v, want %+v", i, got[i].ToolCalls, want[i].ToolCalls)
		}
	}
}

func TestLoadHistoryEmptySession(t *testing.T) {
	s := openTestStore(t)
	sid, err := s.CreateSession("m")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.LoadHistory(sid)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d messages for a fresh session, want 0", len(got))
	}
}

func TestSessionExists(t *testing.T) {
	s := openTestStore(t)
	sid, err := s.CreateSession("m")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if exists, err := s.SessionExists(sid); err != nil || exists {
		t.Errorf("SessionExists = %v, %v before the first message, want false", exists, err)
	}
	if err := s.AppendMessage(sid, model.Message{Role: model.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	exists, err := s.SessionExists(sid)
	if err != nil {
		t.Fatalf("SessionExists: %v", err)
	}
	if !exists {
		t.Error("SessionExists = false for a session with a message")
	}

	exists, err = s.SessionExists("no-such-id")
	if err != nil {
		t.Fatalf("SessionExists: %v", err)
	}
	if exists {
		t.Error("SessionExists = true for an unknown id")
	}
}

func TestLoadHistoryIsolatesSessions(t *testing.T) {
	s := openTestStore(t)
	sidA, _ := s.CreateSession("m")
	sidB, _ := s.CreateSession("m")

	if err := s.AppendMessage(sidA, model.Message{Role: model.RoleUser, Content: "in A"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(sidB, model.Message{Role: model.RoleUser, Content: "in B"}); err != nil {
		t.Fatal(err)
	}

	gotA, err := s.LoadHistory(sidA)
	if err != nil {
		t.Fatalf("LoadHistory(A): %v", err)
	}
	if len(gotA) != 1 || gotA[0].Content != "in A" {
		t.Errorf("session A history = %+v, want just \"in A\"", gotA)
	}
}
