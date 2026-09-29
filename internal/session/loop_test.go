package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

// The store as the loop uses it: a turn is persisted as it happens, masked
// output and a compaction are recorded, and a loop resuming the session
// starts where the first left off.
func TestLoopResumesFromTheStore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	id, _ := s.CreateSession("m")

	big := strings.Repeat("line of output\n", 200)
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c1", Name: "echo", Args: json.RawMessage(`{}`)}}, {Kind: model.EventDone}},
		{{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventDone}},
	}}
	l := &core.Loop{Provider: p, Tools: tool.NewRegistry(bigEcho{big}), Store: s, SessionID: id}
	if _, err := l.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if err := l.Fit(context.Background()); err != nil { // nothing to do: no window
		t.Fatal(err)
	}
	if err := s.ElideMessage(id, 2, "c1", "[echo: masked]"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCompaction(id, core.Compaction{FirstKept: 3, Summary: "## Goal\n- echo"}); err != nil {
		t.Fatal(err)
	}

	history, err := s.Resume(id)
	if err != nil || len(history) != 4 || history[2].Content != big || history[2].Elided != "[echo: masked]" {
		t.Fatalf("resumed history = %d messages, %v", len(history), err)
	}
	resumed := &core.Loop{Store: s, SessionID: id, History: history}
	if err := resumed.LoadCompaction(); err != nil || resumed.Compacted == nil || resumed.Compacted.FirstKept != 3 {
		t.Fatalf("LoadCompaction = %v, compacted %+v", err, resumed.Compacted)
	}
	if first := resumed.Messages()[0].Content; !strings.Contains(first, "- echo") {
		t.Errorf("resumed request starts with %.80q, want the summary", first)
	}
}

// bigEcho is a read-only tool returning out.
type bigEcho struct{ out string }

func (bigEcho) Schema() model.ToolSchema { return model.ToolSchema{Name: "echo"} }
func (bigEcho) Risky() bool              { return false }
func (b bigEcho) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: b.out}, nil
}
