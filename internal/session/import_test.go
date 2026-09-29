package session

import (
	"path/filepath"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func TestImportScopesSessionsToDir(t *testing.T) {
	dir := t.TempDir()
	old, err := Open(filepath.Join(dir, "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := old.CreateSession("m")
	if err := old.AppendMessage(id, model.Message{Role: model.RoleUser, Content: "from the project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`INSERT INTO spans VALUES ('sp', '', ?, 'turn', '', 1, 0, '', '{}')`, id); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()

	s, err := Open(filepath.Join(dir, "global.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	s.Dir = "/elsewhere"
	if _, err := s.CreateSession("m"); err != nil {
		t.Fatal(err)
	}
	for range 2 { // a rerun imports nothing twice
		if err := s.Import(filepath.Join(dir, "old.db"), "/proj"); err != nil {
			t.Fatal(err)
		}
	}

	s.Dir = "/proj"
	list, err := s.ListSessions()
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].Preview != "from the project" {
		t.Fatalf("sessions in /proj = %+v, %v", list, err)
	}
	if h, err := s.LoadHistory(id); err != nil || len(h) != 1 {
		t.Errorf("history = %+v, %v", h, err)
	}
	var spans int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM spans WHERE session_id = ?`, id).Scan(&spans); err != nil || spans != 1 {
		t.Errorf("spans = %d, %v; want the one imported", spans, err)
	}
	s.Dir = ""
	if all, _ := s.ListSessions(); len(all) != 2 {
		t.Errorf("all sessions = %+v, want 2", all)
	}
}
