package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

type elideStore struct {
	appended int
	elided   map[int]string
}

func (s *elideStore) AppendMessage(string, model.Message) error { s.appended++; return nil }
func (s *elideStore) ElideMessage(_ string, index int, id, stub string) error {
	s.elided[index] = id + " " + stub
	return nil
}
func (s *elideStore) ElideToolCalls(_ string, index int, calls []model.ToolCall) error {
	b, _ := json.Marshal(calls)
	s.elided[index] = string(b)
	return nil
}

// overflowOnce rejects the first request as too long, then answers.
type overflowOnce struct {
	reqs []model.Request
}

func (p *overflowOnce) Stream(_ context.Context, req model.Request) (<-chan model.Event, error) {
	p.reqs = append(p.reqs, req)
	if len(p.reqs) == 1 {
		return nil, fmt.Errorf("400: maximum context length exceeded: %w", model.ErrContextOverflow)
	}
	ch := make(chan model.Event, 2)
	ch <- model.Event{Kind: model.EventTextDelta, Text: "ok"}
	ch <- model.Event{Kind: model.EventDone}
	close(ch)
	return ch, nil
}

func TestElisionKeepsHandleAndIsStored(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // masked output is saved there
	big := strings.Repeat("line of output\n", 200)
	store := &elideStore{elided: map[int]string{}}
	p := &overflowOnce{}
	l := &Loop{Provider: p, Store: store, SessionID: "s", History: []model.Message{
		{Role: model.RoleUser, Content: "look"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "r", Name: "read", Args: json.RawMessage(`{"path":"internal/core/loop.go"}`)},
			{ID: "b", Name: "bash", Args: json.RawMessage(`{"command":"go test ./..."}`)},
		}},
		{Role: model.RoleTool, ToolCallID: "r", Content: big},
		{Role: model.RoleTool, ToolCallID: "b", Content: big + "FAIL loop_test.go:1\n[exit code 1]", IsError: true},
		{Role: model.RoleAssistant, Content: "seen"},
	}}

	if _, err := l.Run(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	retry := p.reqs[1].Messages
	read, bash := retry[2].Content, retry[3].Content
	if !strings.HasPrefix(read, "[read internal/core/loop.go: 2.9 KB masked") || !strings.Contains(read, "call read again") {
		t.Errorf("read stand-in = %q", read)
	}
	if !strings.HasPrefix(bash, `[bash "go test ./...": exit 1, 3.0 KB masked; full output in `) || !strings.HasSuffix(bash, "\nlast line: FAIL loop_test.go:1]") {
		t.Errorf("bash stand-in = %q", bash)
	}
	if spill, err := os.ReadFile(spillFrom(bash)); err != nil || string(spill) != l.History[3].Content {
		t.Errorf("spill file: %v; want the full output", err)
	}
	if l.History[2].Content != big {
		t.Error("full output was not kept in history")
	}
	if !strings.HasPrefix(store.elided[2], "r [read") || !strings.HasPrefix(store.elided[3], "b [bash") {
		t.Errorf("stored stand-ins = %v", store.elided)
	}
}
