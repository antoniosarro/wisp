package session

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/antoniosarro/wisp/internal/core"
)

var _ core.Checkpointer = (*Store)(nil)

// A checkpoint is a file as it was before a turn first changed it. Undoing
// turns writes the oldest checkpoint of each file back and drops the turns'
// messages; checkpoints stay in the database, not the project.

// SaveFile records path as it is now, before the turn whose user message
// is the session's turn-th changes it. Only the first save of a file in a
// turn is kept: later ones would hold the turn's own edits.
func (s *Store) SaveFile(sessionID string, turn int, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	var one int
	err = s.db.QueryRow(`SELECT 1 FROM checkpoints WHERE session_id = ? AND turn = ? AND path = ?`, sessionID, turn, abs).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("looking up checkpoint: %w", err)
	}
	existed, content, mode := true, []byte{}, fs.FileMode(0o644)
	if info, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
		existed = false
	} else if err != nil {
		return err
	} else if content, err = os.ReadFile(abs); err != nil {
		return err
	} else {
		mode = info.Mode().Perm()
	}
	_, err = s.db.Exec(
		`INSERT OR IGNORE INTO checkpoints (session_id, turn, path, existed, content, mode) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, turn, abs, existed, content, int(mode),
	)
	if err != nil {
		return fmt.Errorf("saving checkpoint: %w", err)
	}
	return nil
}

// RestoreFiles puts every file the session's turns from the turn-th
// message on changed back as it was before them, deleting the files they
// created, and forgets their checkpoints. It returns the paths restored.
func (s *Store) RestoreFiles(sessionID string, turn int) ([]string, error) {
	type checkpoint struct {
		path    string
		existed bool
		content []byte
		mode    int
	}
	// Oldest first: a file's first checkpoint is how it was before them all.
	rows, err := s.db.Query(
		`SELECT path, existed, content, mode FROM checkpoints WHERE session_id = ? AND turn >= ? ORDER BY turn, path`,
		sessionID, turn,
	)
	if err != nil {
		return nil, fmt.Errorf("loading checkpoints: %w", err)
	}
	var oldest []checkpoint
	seen := map[string]bool{}
	for rows.Next() {
		var c checkpoint
		if err := rows.Scan(&c.path, &c.existed, &c.content, &c.mode); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scanning checkpoint: %w", err)
		}
		if !seen[c.path] {
			seen[c.path] = true
			oldest = append(oldest, c)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading checkpoints: %w", err)
	}

	var restored []string
	for _, c := range oldest {
		if !c.existed {
			err = os.Remove(c.path)
			if errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
		} else if err = os.MkdirAll(filepath.Dir(c.path), 0o755); err == nil {
			err = os.WriteFile(c.path, c.content, fs.FileMode(c.mode))
		}
		if err != nil {
			return restored, fmt.Errorf("restoring %s: %w", c.path, err)
		}
		restored = append(restored, c.path)
	}
	if _, err := s.db.Exec(`DELETE FROM checkpoints WHERE session_id = ? AND turn >= ?`, sessionID, turn); err != nil {
		return restored, fmt.Errorf("deleting checkpoints: %w", err)
	}
	return restored, nil
}

// TruncateHistory drops the session's messages from the n-th on.
func (s *Store) TruncateHistory(sessionID string, n int) error {
	_, err := s.db.Exec(
		`DELETE FROM messages WHERE id IN (SELECT id FROM messages WHERE session_id = ? ORDER BY id LIMIT -1 OFFSET ?)`,
		sessionID, n,
	)
	if err != nil {
		return fmt.Errorf("truncating history: %w", err)
	}
	return nil
}
