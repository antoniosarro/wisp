package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
	"github.com/antoniosarro/wisp/internal/tool/builtin"
)

// call is a step making one tool call with args.
func call(id, name string, args map[string]string) []model.Event {
	b, _ := json.Marshal(args)
	return []model.Event{{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: id, Name: name, Args: b}}, {Kind: model.EventDone}}
}

func answer(text string) []model.Event {
	return []model.Event{{Kind: model.EventTextDelta, Text: text}, {Kind: model.EventDone}}
}

// shell stands in for bash: risky, and its changes aren't tracked.
type shell struct{}

func (shell) Schema() model.ToolSchema { return model.ToolSchema{Name: "sh"} }
func (shell) Risky() bool              { return true }
func (shell) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Undo takes turns back one at a time, files and messages together: each
// file returns to how it was before the turn's first change, files the
// turn created are deleted, and tools it can't track are named.
func TestUndoRestoresFilesAndHistory(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateSession("m")
	dir := t.TempDir()
	a, empty, created := filepath.Join(dir, "a.txt"), filepath.Join(dir, "empty.txt"), filepath.Join(dir, "sub", "new.txt")
	if err := os.WriteFile(a, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		call("c1", "write", map[string]string{"path": a, "content": "two"}),
		answer("first done"),
		call("c2", "edit", map[string]string{"path": a, "old_string": "two", "new_string": "three"}),
		call("c3", "write", map[string]string{"path": created, "content": "x"}),
		call("c4", "write", map[string]string{"path": empty, "content": "full"}),
		call("c5", "sh", map[string]string{}),
		call("c6", "write", map[string]string{"path": a, "content": "four"}),
		answer("second done"),
	}}
	l := &core.Loop{Provider: p, Tools: tool.NewRegistry(builtin.WriteTool{}, builtin.EditTool{}, shell{}), Store: s, SessionID: id}
	for _, input := range []string{"first", "second"} {
		if _, err := l.Run(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	if got := readFile(t, a); got != "four" {
		t.Fatalf("a.txt before undo = %q", got)
	}

	u, err := l.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if u.Input != "second" || len(u.Files) != 3 || !slices.Equal(u.Untracked, []string{"sh"}) {
		t.Errorf("Undo = %+v", u)
	}
	if got := readFile(t, a); got != "two" {
		t.Errorf("a.txt = %q, want two", got)
	}
	if got := readFile(t, empty); got != "" {
		t.Errorf("empty.txt = %q, want empty", got)
	}
	if _, err := os.Stat(created); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("created file still there: %v", err)
	}
	if info, _ := os.Stat(a); info.Mode().Perm() != 0o600 {
		t.Errorf("a.txt mode = %v, want 0600", info.Mode().Perm())
	}
	stored, err := s.LoadHistory(id)
	if err != nil || len(stored) != 4 || len(l.History) != 4 || stored[3].Content != "first done" {
		t.Fatalf("after undo: %d stored, %d in the loop, %v", len(stored), len(l.History), err)
	}

	if u, err = l.Undo(); err != nil || u.Input != "first" || len(u.Untracked) != 0 {
		t.Fatalf("second Undo = %+v, %v", u, err)
	}
	if got := readFile(t, a); got != "one" {
		t.Errorf("a.txt = %q, want one", got)
	}
	if stored, _ = s.LoadHistory(id); len(stored) != 0 || len(l.History) != 0 {
		t.Errorf("after undoing everything: %d stored, %d in the loop", len(stored), len(l.History))
	}
	if _, err := l.Undo(); !errors.Is(err, core.ErrNothingToUndo) {
		t.Errorf("Undo of an empty session = %v", err)
	}
}

// A turn the latest summary covers can't be undone.
func TestUndoStopsAtCompaction(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateSession("m")
	l := &core.Loop{Provider: &testutil.ScriptedProvider{Turns: [][]model.Event{answer("ok")}}, Store: s, SessionID: id}
	if _, err := l.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	l.Compacted = &core.Compaction{FirstKept: 2}
	if _, err := l.Undo(); !errors.Is(err, core.ErrUndoCompacted) {
		t.Errorf("Undo = %v, want ErrUndoCompacted", err)
	}
	if len(l.History) != 2 {
		t.Errorf("history has %d messages, want 2", len(l.History))
	}
}
