package session

import (
	"context"
	"fmt"
	"strings"
)

// Import copies every session in the database at path, which older
// versions kept in each project, into s as sessions of dir. Sessions
// already present are skipped, so an interrupted import can be rerun.
func (s *Store) Import(path, dir string) error {
	old, err := Open(path) // brings its schema up to date
	if err != nil {
		return err
	}
	_ = old.Close()
	ctx := context.Background()
	// ATTACH applies to one connection, so the import holds one.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS old`, path); err != nil {
		return fmt.Errorf("attaching %s: %w", path, err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, `DETACH DATABASE old`) }()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Sessions are copied last, so a session present in main means all its
	// rows are too.
	const fresh = `session_id NOT IN (SELECT id FROM main.sessions)`
	for _, q := range []string{
		`INSERT INTO main.messages (session_id,role,content,tool_calls,tool_call_id,created_at,is_error,elided)
			SELECT session_id,role,content,tool_calls,tool_call_id,created_at,is_error,elided FROM old.messages WHERE ` + fresh + ` ORDER BY id`,
		`INSERT OR IGNORE INTO main.spans SELECT id,parent_id,session_id,kind,name,start_ns,end_ns,status,attrs FROM old.spans WHERE ` + fresh,
		`INSERT INTO main.compactions (session_id,first_kept,summary,ledger,tokens_before,tokens_after,created_at,precomputed,files)
			SELECT session_id,first_kept,summary,ledger,tokens_before,tokens_after,created_at,precomputed,files FROM old.compactions WHERE ` + fresh + ` ORDER BY id`,
		`INSERT OR IGNORE INTO main.sessions (id,created_at,model,dir) SELECT id,created_at,model,? FROM old.sessions ORDER BY rowid`,
	} {
		var args []any
		if strings.Contains(q, "?") {
			args = append(args, dir)
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("importing %s: %w", path, err)
		}
	}
	return tx.Commit()
}
