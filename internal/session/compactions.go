package session

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/antoniosarro/wisp/internal/core"
)

// The store keeps every compaction of a session, so a resumed session
// starts from its latest summary (core.Loop.LoadCompaction) and frontends
// can mark each one.

// SaveCompaction records a compaction of the session's history.
func (s *Store) SaveCompaction(sessionID string, c core.Compaction) error {
	ledger, err := json.Marshal(c.Ledger)
	if err != nil {
		return fmt.Errorf("encoding ledger: %w", err)
	}
	files, err := json.Marshal(c.Files)
	if err != nil {
		return fmt.Errorf("encoding files: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO compactions (session_id, first_kept, summary, ledger, tokens_before, tokens_after, precomputed, files, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, c.FirstKept, c.Summary, string(ledger), c.TokensBefore, c.TokensAfter, c.Precomputed, string(files), time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("saving compaction: %w", err)
	}
	return nil
}

// Compactions returns the session's compactions, oldest first.
func (s *Store) Compactions(sessionID string) ([]core.Compaction, error) {
	rows, err := s.db.Query(
		`SELECT first_kept, summary, ledger, tokens_before, tokens_after, precomputed, files FROM compactions WHERE session_id = ? ORDER BY id`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading compactions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var all []core.Compaction
	for rows.Next() {
		var c core.Compaction
		var ledger, files string
		if err := rows.Scan(&c.FirstKept, &c.Summary, &ledger, &c.TokensBefore, &c.TokensAfter, &c.Precomputed, &files); err != nil {
			return nil, fmt.Errorf("scanning compaction: %w", err)
		}
		if err := json.Unmarshal([]byte(ledger), &c.Ledger); err != nil {
			return nil, fmt.Errorf("decoding ledger: %w", err)
		}
		if files != "" {
			if err := json.Unmarshal([]byte(files), &c.Files); err != nil {
				return nil, fmt.Errorf("decoding files: %w", err)
			}
		}
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading compactions: %w", err)
	}
	return all, nil
}
