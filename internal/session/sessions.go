package session

import (
	"crypto/rand"
	"fmt"
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
}

// OneLinePreview names the session in lists: the start of its first
// prompt on one line.
func (s Summary) OneLinePreview() string {
	return strings.ReplaceAll(s.Preview, "\n", " ")
}

// ListSessions returns the 100 most recent sessions of s.Dir, or of every
// directory when it is "", newest first.
func (s *Store) ListSessions() ([]Summary, error) {
	rows, err := s.db.Query(`SELECT s.id,s.model,s.created_at,s.dir,COALESCE((SELECT substr(content,1,100) FROM messages WHERE session_id=s.id AND role='user' ORDER BY id LIMIT 1),'') FROM sessions s WHERE ?1='' OR s.dir=?1 ORDER BY s.created_at DESC,s.rowid DESC LIMIT 100`, s.Dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []Summary
	for rows.Next() {
		var s Summary
		var created int64
		if err := rows.Scan(&s.ID, &s.Model, &created, &s.Dir, &s.Preview); err != nil {
			return nil, err
		}
		s.CreatedAt = time.Unix(created, 0)
		result = append(result, s)
	}
	return result, rows.Err()
}

// CreateSession inserts a new session of s.Dir and returns its id, a
// random string (older versions used UUIDs; both work as ids).
func (s *Store) CreateSession(modelName string) (string, error) {
	id := strings.ToLower(rand.Text())
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, created_at, model, dir) VALUES (?, ?, ?, ?)`,
		id, time.Now().Unix(), modelName, s.Dir,
	)
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	return id, nil
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
	var name string
	if err := s.db.QueryRow(`SELECT model FROM sessions WHERE id = ?`, id).Scan(&name); err != nil {
		return "", fmt.Errorf("reading session %s: %w", id, err)
	}
	return name, nil
}

// SetSessionModel records the model a session now uses, so resuming it
// picks the same one.
func (s *Store) SetSessionModel(id, name string) error {
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
