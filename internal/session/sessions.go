package session

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

// Summary describes a session for lists.
type Summary struct {
	ID        string
	Model     string
	CreatedAt time.Time
	Preview   string // start of the first user prompt
	Dir       string // the directory it was started in
	Title     string // the name given with RenameSession; "" for none
	Starred   bool   // listed first (SetStarred)
}

// OneLinePreview names the session in lists: its title, or the start of
// its first prompt, on one line.
func (s Summary) OneLinePreview() string {
	if s.Title != "" {
		return s.Title
	}
	return strings.ReplaceAll(s.Preview, "\n", " ")
}

// ListSessions returns the 100 most recent sessions of s.Dir, or of every
// directory when it is "", starred ones first, then newest first.
func (s *Store) ListSessions() ([]Summary, error) {
	rows, err := s.db.Query(`SELECT s.id,s.model,s.created_at,s.dir,COALESCE(s.title,''),s.starred,COALESCE((SELECT substr(content,1,100) FROM messages WHERE session_id=s.id AND role='user' ORDER BY id LIMIT 1),'') FROM sessions s WHERE ?1='' OR s.dir=?1 ORDER BY s.starred DESC,s.created_at DESC,s.rowid DESC LIMIT 100`, s.Dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []Summary
	for rows.Next() {
		var s Summary
		var created int64
		if err := rows.Scan(&s.ID, &s.Model, &created, &s.Dir, &s.Title, &s.Starred, &s.Preview); err != nil {
			return nil, err
		}
		s.CreatedAt = time.Unix(created, 0)
		result = append(result, s)
	}
	return result, rows.Err()
}

// CreateSession starts a new session of s.Dir and returns its id, a random
// string (older versions used UUIDs; both work as ids). The session is
// stored, and listed, from its first message on (AppendMessage).
func (s *Store) CreateSession(modelName string) (string, error) {
	id := strings.ToLower(rand.Text())
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = map[string]*pendingSession{}
	}
	s.pending[id] = &pendingSession{model: modelName, dir: s.Dir}
	return id, nil
}

// materialize gives a pending session its row, created now.
func (s *Store) materialize(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[id]
	if !ok {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, created_at, model, dir, title) VALUES (?, ?, ?, ?, ?)`,
		id, time.Now().Unix(), p.model, p.dir, p.title,
	)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	delete(s.pending, id)
	return nil
}

// RenameSession gives a session a title, which lists show in place of its
// first prompt; "" removes it.
func (s *Store) RenameSession(id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pending[id]; ok {
		p.title = title
		return nil
	}
	res, err := s.db.Exec(`UPDATE sessions SET title = ? WHERE id = ?`, title, id)
	if err != nil {
		return fmt.Errorf("renaming session %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no such session: %s", id)
	}
	return nil
}

// SetStarred stars a session, which lists show first, or unstars it.
func (s *Store) SetStarred(id string, starred bool) error {
	res, err := s.db.Exec(`UPDATE sessions SET starred = ? WHERE id = ?`, starred, id)
	if err != nil {
		return fmt.Errorf("starring session %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no such session: %s", id)
	}
	return nil
}

// DeleteSession deletes a session and everything stored with it: its
// messages, summaries, spans, file checkpoints, and pastes.
func (s *Store) DeleteSession(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // a no-op after Commit
	for _, table := range []string{"messages", "compactions", "spans", "checkpoints", "pastes", "sessions"} {
		col := "session_id"
		if table == "sessions" {
			col = "id"
		}
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE `+col+` = ?`, id); err != nil {
			return fmt.Errorf("deleting session %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return os.RemoveAll(s.pasteDir(id))
}

// SessionTitle returns a session's title, "" when it has none.
func (s *Store) SessionTitle(id string) (string, error) {
	s.mu.Lock()
	p, ok := s.pending[id]
	s.mu.Unlock()
	if ok {
		return p.title, nil
	}
	var title string
	// COALESCE: older versions had a nullable title column of their own.
	err := s.db.QueryRow(`SELECT COALESCE(title,'') FROM sessions WHERE id = ?`, id).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return title, err
}

// SessionExists reports whether id refers to a known session.
func (s *Store) SessionExists(id string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)`, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking session %s: %w", id, err)
	}
	return exists, nil
}

// SessionModel returns the model a session last used.
func (s *Store) SessionModel(id string) (string, error) {
	s.mu.Lock()
	p, ok := s.pending[id]
	s.mu.Unlock()
	if ok {
		return p.model, nil
	}
	var name string
	if err := s.db.QueryRow(`SELECT model FROM sessions WHERE id = ?`, id).Scan(&name); err != nil {
		return "", fmt.Errorf("reading session %s: %w", id, err)
	}
	return name, nil
}

// SetSessionModel records the model a session now uses, so resuming it
// picks the same one.
func (s *Store) SetSessionModel(id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pending[id]; ok {
		p.model = name
		return nil
	}
	if _, err := s.db.Exec(`UPDATE sessions SET model = ? WHERE id = ?`, name, id); err != nil {
		return fmt.Errorf("updating session %s: %w", id, err)
	}
	return nil
}

// Resume loads the history of an existing session, failing for unknown ids
// rather than silently starting empty.
func (s *Store) Resume(id string) ([]model.Message, error) {
	exists, err := s.SessionExists(id)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("no such session: %s", id)
	}
	return s.LoadHistory(id)
}
