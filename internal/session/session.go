// Package session persists conversations to SQLite, through a pure-Go
// driver so wisp stays a static binary. One database holds every
// project's sessions, each tagged with the directory it was started in:
//   - session.go: the schema, opening, and migrations
//   - sessions.go: creating, listing, and resuming sessions
//   - messages.go: a session's history, masked output included
//   - compactions.go: its summaries, so a resume starts from the latest
//   - checkpoints.go: files as they were before each turn changed them,
//     and undoing a turn
//   - pastes.go: the labels of long pastes and pasted images
package session

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// schema creates the tables of a new database. Columns added since the
// first version are in columns, so old databases gain them too.
const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	created_at INTEGER NOT NULL, -- when its first message was stored
	model      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id   TEXT NOT NULL REFERENCES sessions(id),
	role         TEXT NOT NULL,
	content      TEXT NOT NULL,
	tool_calls   TEXT,
	tool_call_id TEXT,
	created_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS messages_session ON messages(session_id, id);

CREATE TABLE IF NOT EXISTS spans (
	id         TEXT PRIMARY KEY,
	parent_id  TEXT NOT NULL,
	session_id TEXT NOT NULL,
	kind       TEXT NOT NULL,
	name       TEXT NOT NULL,
	start_ns   INTEGER NOT NULL,
	end_ns     INTEGER NOT NULL, -- 0 while the span is open
	status     TEXT NOT NULL,
	attrs      TEXT NOT NULL      -- JSON object
);

CREATE INDEX IF NOT EXISTS spans_session ON spans(session_id, start_ns);

CREATE TABLE IF NOT EXISTS compactions (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id    TEXT NOT NULL REFERENCES sessions(id),
	first_kept    INTEGER NOT NULL, -- index of the first message kept verbatim
	summary       TEXT NOT NULL,    -- the model's sections
	ledger        TEXT NOT NULL,    -- core.Ledger as JSON
	tokens_before INTEGER NOT NULL,
	tokens_after  INTEGER NOT NULL,
	created_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS compactions_session ON compactions(session_id, id);

CREATE TABLE IF NOT EXISTS checkpoints (
	session_id TEXT NOT NULL,
	turn       INTEGER NOT NULL, -- index of the turn's user message
	path       TEXT NOT NULL,    -- absolute
	existed    INTEGER NOT NULL, -- 0: the turn created the file
	content    BLOB NOT NULL,
	mode       INTEGER NOT NULL,
	PRIMARY KEY (session_id, turn, path)
);

CREATE TABLE IF NOT EXISTS pastes (
	session_id TEXT NOT NULL,
	label      TEXT NOT NULL,
	text       TEXT NOT NULL, -- "" for an image
	path       TEXT NOT NULL, -- the image's file; "" for text
	PRIMARY KEY (session_id, label)
);
`

// columns are added to databases created by older versions; SQLite has no
// ADD COLUMN IF NOT EXISTS, so each is added only when missing.
var columns = []struct{ table, name, ddl string }{
	{"messages", "is_error", `ALTER TABLE messages ADD COLUMN is_error INTEGER NOT NULL DEFAULT 0`},
	{"messages", "elided", `ALTER TABLE messages ADD COLUMN elided TEXT`},
	{"compactions", "precomputed", `ALTER TABLE compactions ADD COLUMN precomputed INTEGER NOT NULL DEFAULT 0`},
	{"compactions", "files", `ALTER TABLE compactions ADD COLUMN files TEXT NOT NULL DEFAULT ''`},
	{"sessions", "dir", `ALTER TABLE sessions ADD COLUMN dir TEXT NOT NULL DEFAULT ''`},
	{"sessions", "title", `ALTER TABLE sessions ADD COLUMN title TEXT NOT NULL DEFAULT ''`},
}

// Store wraps a SQLite session database. Its methods are safe for
// concurrent use, as database/sql is; set Dir before sharing it.
type Store struct {
	db   *sql.DB
	path string // the database file; pasted images are kept beside it
	// Dir is the project directory new sessions belong to and
	// ListSessions shows; "" lists every session.
	Dir string

	mu sync.Mutex
	// pending are sessions created but without a message yet: they get a
	// row with their first message, so starting wisp and leaving leaves no
	// empty session behind.
	pending map[string]*pendingSession
}

// pendingSession is what a session's row will hold once it has one; dir
// is Dir when it was created.
type pendingSession struct{ model, title, dir string }

// Open creates or opens the database at path and ensures the schema exists.
// WAL and a busy timeout let several wisp processes share one database.
func Open(path string) (*Store, error) {
	// Whole transcripts, tool output included: for the user's eyes only,
	// from the moment the file exists. SQLite gives the WAL files the
	// database's mode.
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
		_ = f.Close()
	} else if !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("creating %s: %w", path, err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pinging %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	// A database an older version created may be readable by others.
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Chmod(f, 0o600)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

// migrate adds the columns an older database lacks.
func migrate(db *sql.DB) error {
	for _, c := range columns {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.name).Scan(&n)
		if err == nil && n == 0 {
			// Another wisp opening the same old database may add it first.
			if _, err = db.Exec(c.ddl); err != nil && strings.Contains(err.Error(), "duplicate column name") {
				err = nil
			}
		}
		if err != nil {
			return fmt.Errorf("migrating schema: %w", err)
		}
	}
	return nil
}

// Close closes the database, first deleting what sessions that never got a
// message recorded, such as the spans of MCP servers starting, or a paste
// never sent.
func (s *Store) Close() error {
	s.mu.Lock()
	for id := range s.pending {
		_, _ = s.db.Exec(`DELETE FROM spans WHERE session_id = ?`, id)
		_, _ = s.db.Exec(`DELETE FROM pastes WHERE session_id = ?`, id)
		_ = os.RemoveAll(s.pasteDir(id))
	}
	s.pending = nil
	s.mu.Unlock()
	return s.db.Close()
}
