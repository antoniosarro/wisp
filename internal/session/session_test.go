package session

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/span"
)

func TestOpenCreatesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	for _, table := range []string{"sessions", "messages", "spans"} {
		var name string
		err := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (existing db): %v", err)
	}
	defer func() { _ = s2.Close() }()
}

func TestSessionModel(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	id, err := s.CreateSession("")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionModel(id, "qwen"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionModel(id); err != nil || got != "qwen" {
		t.Fatalf("SessionModel = %q, %v", got, err)
	}
}

var _ core.Elider = (*Store)(nil)

func TestElidedOutputSurvivesResume(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	id, _ := s.CreateSession("m")
	for _, m := range []model.Message{
		{Role: model.RoleUser, Content: "go"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c1", Name: "read"}}},
		{Role: model.RoleTool, ToolCallID: "c1", Content: "full output"},
	} {
		if err := s.AppendMessage(id, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ElideMessage(id, 2, "wrong-id", "nope"); err != nil {
		t.Fatal(err)
	}
	if err := s.ElideMessage(id, 2, "c1", "[read: elided]"); err != nil {
		t.Fatal(err)
	}
	h, err := s.LoadHistory(id)
	if err != nil || h[2].Content != "full output" || h[2].Elided != "[read: elided]" {
		t.Fatalf("history = %+v, %v", h, err)
	}
}

func TestElidedToolCallsSurviveResume(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	id, _ := s.CreateSession("m")
	calls := []model.ToolCall{{ID: "c1", Name: "write", Args: json.RawMessage(`{"path":"f","content":"long"}`)}}
	for _, m := range []model.Message{
		{Role: model.RoleUser, Content: "go"},
		{Role: model.RoleAssistant, ToolCalls: calls},
		{Role: model.RoleTool, ToolCallID: "c1", Content: "ok"},
	} {
		if err := s.AppendMessage(id, m); err != nil {
			t.Fatal(err)
		}
	}
	calls[0].Elided = json.RawMessage(`{"path":"f","content":"<masked>"}`)
	if err := s.ElideToolCalls(id, 2, calls); err != nil { // the tool row: ignored
		t.Fatal(err)
	}
	if err := s.ElideToolCalls(id, 1, calls); err != nil {
		t.Fatal(err)
	}
	h, err := s.LoadHistory(id)
	if err != nil || len(h) != 3 || h[2].Content != "ok" {
		t.Fatalf("history = %+v, %v", h, err)
	}
	if got := h[1].ToolCalls[0]; string(got.Args) != `{"path":"f","content":"long"}` || string(got.Elided) != `{"path":"f","content":"<masked>"}` {
		t.Errorf("tool call = %s / %s", got.Args, got.Elided)
	}
}

var _ core.Compactor = (*Store)(nil)

func TestCompactionSurvivesResume(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	id, _ := s.CreateSession("m")
	if all, err := s.Compactions(id); err != nil || all != nil {
		t.Fatalf("new session: %v, %v", all, err)
	}
	var lg core.Ledger
	lg.Update([]model.Message{{Role: model.RoleUser, Content: "keep this"}})
	for _, first := range []int{4, 9} {
		files := []core.FileSnapshot{{Path: "a.go", Content: "     1\tpackage a"}}
		if err := s.SaveCompaction(id, core.Compaction{FirstKept: first, Summary: "## Goal", Ledger: lg, TokensBefore: 900, TokensAfter: 100, Precomputed: true, Files: files}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Compactions(id)
	if err != nil || len(all) != 2 || all[0].FirstKept != 4 {
		t.Fatalf("compactions = %+v, %v", all, err)
	}
	c := all[1]
	if c.FirstKept != 9 || c.Summary != "## Goal" || c.TokensBefore != 900 || c.TokensAfter != 100 || len(c.Ledger.User) != 1 || !c.Precomputed || len(c.Files) != 1 || c.Files[0].Path != "a.go" {
		t.Fatalf("latest = %+v, %v", c, err)
	}
}

func TestCorruptToolCallsDoNotBreakResume(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	id, _ := s.CreateSession("m")
	_ = s.AppendMessage(id, model.Message{Role: model.RoleUser, Content: "go"})
	_ = s.AppendMessage(id, model.Message{Role: model.RoleAssistant, Content: "calling", ToolCalls: []model.ToolCall{{ID: "c1", Name: "read"}}})
	_ = s.AppendMessage(id, model.Message{Role: model.RoleTool, ToolCallID: "c1", Content: "out"})
	if _, err := s.db.Exec(`UPDATE messages SET tool_calls = '{broken' WHERE role = 'assistant'`); err != nil {
		t.Fatal(err)
	}
	h, err := s.LoadHistory(id)
	if err != nil || len(h) != 3 || !strings.Contains(h[1].Content, "could not be restored") {
		t.Fatalf("history = %+v, %v; want every message kept, indices unchanged", h, err)
	}
	if len(h[1].ToolCalls) != 1 || h[1].ToolCalls[0].ID != "c1" || h[2].ToolCallID != "c1" {
		t.Errorf("the result's call wasn't restored: %+v", h[1].ToolCalls)
	}
}

func TestOpenMigratesOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// title as an older version made it: nullable, and NULL.
	_, err = db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, created_at INTEGER NOT NULL, model TEXT NOT NULL, title TEXT);
		CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL,
			content TEXT NOT NULL, tool_calls TEXT, tool_call_id TEXT, created_at INTEGER NOT NULL);
		INSERT INTO sessions VALUES ('s', 0, 'm', NULL);
		INSERT INTO messages (session_id, role, content, created_at) VALUES ('s', 'user', 'old', 0);`)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second open finds the columns already there
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		h, err := s.LoadHistory("s")
		list, listErr := s.ListSessions()
		title, titleErr := s.SessionTitle("s")
		_ = s.Close()
		if err != nil || len(h) != 1 || h[0].Content != "old" {
			t.Fatalf("history = %+v, %v", h, err)
		}
		if listErr != nil || len(list) != 1 || list[0].OneLinePreview() != "old" || titleErr != nil || title != "" {
			t.Fatalf("with a NULL title: sessions %+v, %v; title %q, %v", list, listErr, title, titleErr)
		}
	}
}

func TestOpenKeepsTranscriptsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("session.db mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

// A database created by an older version, readable by others, is made
// private when opened.
func TestOpenMakesOldDatabasesPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("session.db mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

// Starting wisp and leaving left an empty session in every list: a
// session is stored from its first message, and what it recorded before
// one (MCP servers' spans) goes when the store closes.
func TestSessionStoredFromFirstMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Dir = "/p"
	empty, _ := s.CreateSession("m")
	used, _ := s.CreateSession("m")
	for _, id := range []string{empty, used} {
		if err := s.WriteSpan(span.Record{ID: "span-" + id, Session: id, Kind: "mcp.connect", Name: "srv", Start: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if list, _ := s.ListSessions(); len(list) != 0 {
		t.Fatalf("sessions before any message = %+v, want none", list)
	}
	_ = s.SetSessionModel(used, "m2") // /model before the first prompt
	if err := s.AppendMessage(used, model.Message{Role: model.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListSessions(); len(list) != 1 || list[0].ID != used || list[0].Model != "m2" {
		t.Fatalf("sessions = %+v, want only the one with a message, on m2", list)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for id, want := range map[string]int{empty: 0, used: 1} {
		if spans, _ := s.Spans(id); len(spans) != want {
			t.Errorf("session %s has %d spans after closing, want %d", id, len(spans), want)
		}
	}
}

func TestRenameSession(t *testing.T) {
	s := openTestStore(t)
	early, _ := s.CreateSession("m")
	if err := s.RenameSession(early, "named first"); err != nil { // before its first message
		t.Fatal(err)
	}
	_ = s.AppendMessage(early, model.Message{Role: model.RoleUser, Content: "the prompt"})
	later, _ := s.CreateSession("m")
	_ = s.AppendMessage(later, model.Message{Role: model.RoleUser, Content: "another prompt"})
	if err := s.RenameSession(later, "parser rewrite"); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	list, _ := s.ListSessions()
	for _, sum := range list {
		labels[sum.ID] = sum.OneLinePreview()
	}
	if labels[early] != "named first" || labels[later] != "parser rewrite" {
		t.Errorf("list labels = %v, want the titles", labels)
	}
	if title, _ := s.SessionTitle(later); title != "parser rewrite" {
		t.Errorf("SessionTitle = %q", title)
	}
	if err := s.RenameSession(later, ""); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListSessions(); list[0].OneLinePreview() != "another prompt" {
		t.Errorf("after clearing the title, label = %q, want the prompt again", list[0].OneLinePreview())
	}
	if err := s.RenameSession("no-such-id", "x"); err == nil {
		t.Error("renaming an unknown session succeeded")
	}
}

func TestListSessionsByDirectory(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	s.Dir = "/a"
	first, _ := s.CreateSession("m")
	_ = s.AppendMessage(first, model.Message{Role: model.RoleUser, Content: "fix the\nparser"})
	second, _ := s.CreateSession("m2")
	s.Dir = "/b"
	third, _ := s.CreateSession("m")
	// Stored from their first message, each in the directory it began in.
	_ = s.AppendMessage(second, model.Message{Role: model.RoleUser, Content: "second"})
	_ = s.AppendMessage(third, model.Message{Role: model.RoleUser, Content: "third"})

	s.Dir = "/a"
	list, err := s.ListSessions()
	if err != nil || len(list) != 2 || list[0].ID != second || list[1].ID != first {
		t.Fatalf("sessions in /a = %+v, %v; want the two, newest first", list, err)
	}
	if got := list[1].OneLinePreview(); got != "fix the parser" || list[1].Dir != "/a" || list[0].Model != "m2" {
		t.Errorf("summary = %+v, preview %q", list[1], got)
	}
	s.Dir = ""
	if all, _ := s.ListSessions(); len(all) != 3 {
		t.Errorf("all sessions = %d, want 3", len(all))
	}
}

func TestResume(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Resume("no-such-id"); err == nil || !strings.Contains(err.Error(), "no such session") {
		t.Errorf("Resume(unknown) = %v, want an error naming it", err)
	}
	id, _ := s.CreateSession("m")
	_ = s.AppendMessage(id, model.Message{Role: model.RoleUser, Content: "hi"})
	if h, err := s.Resume(id); err != nil || len(h) != 1 {
		t.Errorf("Resume = %+v, %v", h, err)
	}
}

func TestStarAndDeleteSession(t *testing.T) {
	s := openTestStore(t)
	var ids []string
	for _, p := range []string{"old", "new"} {
		id, _ := s.CreateSession("m")
		if err := s.AppendMessage(id, model.Message{Role: model.RoleUser, Content: p}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := s.SetStarred(ids[0], true); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListSessions()
	if len(list) != 2 || list[0].ID != ids[0] || !list[0].Starred {
		t.Fatalf("starred session not first: %+v", list)
	}
	if err := s.SaveFile(ids[0], 0, filepath.Join(t.TempDir(), "f")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(ids[0]); err != nil {
		t.Fatal(err)
	}
	if exists, _ := s.SessionExists(ids[0]); exists {
		t.Error("deleted session still exists")
	}
	for _, table := range []string{"messages", "checkpoints"} {
		var n int
		_ = s.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE session_id = ?`, ids[0]).Scan(&n)
		if n != 0 {
			t.Errorf("%d %s rows left", n, table)
		}
	}
	if h, _ := s.LoadHistory(ids[1]); len(h) != 1 {
		t.Error("the other session lost its messages")
	}
	if err := s.SetStarred("nope", true); err == nil {
		t.Error("starring an unknown session should fail")
	}
}
