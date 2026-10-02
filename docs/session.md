# Sessions

Every conversation is stored in one SQLite database for every project, at
`$XDG_DATA_HOME/wisp/session.db` (`~/.local/share/wisp/session.db` by
default), through a pure-Go driver so wisp stays a static binary. The code
is in `internal/session` and `internal/cli/sessions.go`.

- **By directory.** Each session records the directory it was started in,
  and wisp lists only the current directory's sessions. `--resume ID`
  still opens any session by id.
- **Private.** The transcripts hold tool output too, so the directory is
  created with mode 700 and the database with mode 600, before anything is
  written to it. A database an older version left readable, and its WAL
  files, are made private when opened.
- **Shared.** The database runs in WAL mode with a busy timeout, so several
  wisp processes, in one directory or many, can use it at once.
- **Moved from older versions.** A `.wisp/session.db` left by a version
  that kept sessions per project is imported, as this directory's
  sessions, the first time wisp runs there. The import is one transaction;
  the old file is removed once it commits.

## Schema

```sql
CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL,          -- when its first message was stored
    model      TEXT NOT NULL,             -- the model it last used
    dir        TEXT NOT NULL DEFAULT '',  -- working directory it belongs to
    title      TEXT NOT NULL DEFAULT ''   -- its name, from /session-rename
);

CREATE TABLE messages (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id   TEXT NOT NULL REFERENCES sessions(id),
    role         TEXT NOT NULL,  -- user, assistant, tool
    content      TEXT NOT NULL,
    tool_calls   TEXT,           -- JSON []model.ToolCall, on assistant messages that requested tools
    tool_call_id TEXT,           -- on tool-role messages, links back to the call
    created_at   INTEGER NOT NULL,
    is_error     INTEGER NOT NULL DEFAULT 0, -- the tool result was an error
    elided       TEXT            -- stand-in sent instead of a masked tool result
);

CREATE TABLE spans (             -- trace spans, see tracing.md
    id         TEXT PRIMARY KEY,
    parent_id  TEXT NOT NULL,
    session_id TEXT NOT NULL,
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    start_ns   INTEGER NOT NULL,
    end_ns     INTEGER NOT NULL, -- 0 while the span is open
    status     TEXT NOT NULL,
    attrs      TEXT NOT NULL     -- JSON object
);

CREATE TABLE compactions (       -- see compaction.md
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id    TEXT NOT NULL REFERENCES sessions(id),
    first_kept    INTEGER NOT NULL, -- index of the first message kept verbatim
    summary       TEXT NOT NULL,    -- the model's sections
    ledger        TEXT NOT NULL,    -- core.Ledger as JSON
    tokens_before INTEGER NOT NULL,
    tokens_after  INTEGER NOT NULL,
    created_at    INTEGER NOT NULL,
    precomputed   INTEGER NOT NULL DEFAULT 0,
    files         TEXT NOT NULL DEFAULT '' -- rehydrated files, JSON
);
```

- **Migrations.** Tables missing from an older database are created on
  open, and missing columns (`is_error`, `elided`, `precomputed`, `files`,
  `dir`, `title`) are added. A `sessions.title` column from an older
  version, which may hold NULL, is used as it is.
- **A session is stored from its first message.** Starting wisp and
  leaving saves nothing: the id exists from the start (the splash shows
  it, and spans are recorded under it), but the `sessions` row is written
  with the first message, and the spans of a session that never got one
  are deleted when wisp exits.
- **Titles.** `/session-rename NAME` names the current session; lists
  (`--sessions`, `/resume`, the trace page) show the name in place of the
  first prompt.
- **Ids.** New sessions get a random 26-character id; older versions used
  UUIDs. Both work anywhere an id is asked for.
- **Tool calls as JSON.** `messages.tool_calls` mirrors
  `model.Message.ToolCalls` directly, with no separate table. Arguments
  are stored byte for byte as the model wrote them (`<`, `>`, and `&`
  unescaped). When masking elides a call's large arguments, the row is
  rewritten with each call's `Elided` arguments next to its full `Args`.
- **Order** is `ORDER BY id`: messages are only appended, and masking and
  compactions refer to them by their index in that order.
- **Not stored:** reasoning, which is shown but never sent back to the
  model, and images, which a resumed session reads again if it needs them.

## Compaction

Messages are never deleted and keep their full content. Masking stores
stand-ins (`messages.elided`, elided tool-call arguments), and summarizing
adds a row to `compactions`. Requests then send the latest summary in place
of the messages before its `first_kept`, followed by the rest verbatim. See
[compaction.md](compaction.md).

## Resume

`wisp --resume ID`, or `/resume NUMBER|ID-PREFIX` in the TUI (numbers come
from `/sessions`), loads a session's messages back into context and
continues, with the model the session last used unless `--model` says
otherwise.

- **Masked output** is sent as its stored stand-in, and saved output a
  stand-in names is written back if it went missing
  ([compaction.md](compaction.md#stand-ins)).
- **The latest compaction** is restored once the model's window is known,
  since the summary is rendered for it (`Loop.LoadCompaction`).
- **Interrupted turns are repaired** before the next one: tool calls
  without results get an "interrupted" result, and a prompt without an
  answer gets a placeholder answer, because strict endpoints reject
  either.
- **Damaged rows** don't make a session unusable. A message whose tool
  calls can't be decoded keeps its text, with a note, and its results get
  stand-in calls, so every message keeps its index.
