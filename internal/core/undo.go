package core

import (
	"errors"
	"slices"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// Checkpointer is a MessageStore that can also take turns back: it saves
// files before tools change them and restores them on Undo. Run hands tools
// a checkpoint function (tool.WithCheckpoint) when the Store is one.
type Checkpointer interface {
	MessageStore
	// SaveFile records path as it is before the turn whose user message
	// is History[turn] changes it.
	SaveFile(sessionID string, turn int, path string) error
	// RestoreFiles puts the files changed from History[turn] on back,
	// returning their paths.
	RestoreFiles(sessionID string, turn int) ([]string, error)
	// TruncateHistory drops the stored messages from History[n] on.
	TruncateHistory(sessionID string, n int) error
}

// checkpointTools change files only through tool.Checkpoint, so Undo
// restores what they did.
var checkpointTools = []string{"write", "edit", "multi_edit"}

var (
	ErrNothingToUndo = errors.New("nothing to undo")
	ErrUndoStore     = errors.New("undo needs a saved session")
	ErrUndoCompacted = errors.New("can't undo past the latest compaction: its summary covers that turn")
)

// Undone is what Undo took back.
type Undone struct {
	Input string   // the turn's prompt
	Files []string // restored to how they were before it
	// Untracked are the tools that ran in the turn and may have changed
	// things Undo can't restore, such as bash; each is named once.
	Untracked []string
}

// Undo takes the last turn back: files that write, edit and multi_edit
// changed are restored, and the turn's messages are dropped from History
// and the store. It stops at the latest compaction.
func (l *Loop) Undo() (Undone, error) {
	cp, ok := l.Store.(Checkpointer)
	if !ok {
		return Undone{}, ErrUndoStore
	}
	start := l.lastTurn()
	if start < 0 {
		return Undone{}, ErrNothingToUndo
	}
	if l.Compacted != nil && start < l.Compacted.FirstKept {
		return Undone{}, ErrUndoCompacted
	}
	l.StopPresummary() // it summarizes the turn being dropped

	u := Undone{Input: l.History[start].Content}
	for _, msg := range l.History[start:] {
		for _, call := range msg.ToolCalls {
			if slices.Contains(checkpointTools, call.Name) || slices.Contains(u.Untracked, call.Name) {
				continue
			}
			if t, ok := l.Tools.Get(call.Name); ok && tool.IsRisky(t, call.Args) {
				u.Untracked = append(u.Untracked, call.Name)
			}
		}
	}
	files, err := cp.RestoreFiles(l.SessionID, start)
	u.Files = files
	if err != nil {
		return u, err
	}
	if err := cp.TruncateHistory(l.SessionID, start); err != nil {
		return u, err
	}
	l.History = l.History[:start]
	return u, nil
}

// lastTurn is the index of the last prompt the user sent, or -1.
func (l *Loop) lastTurn() int {
	for i := len(l.History) - 1; i >= 0; i-- {
		if msg := l.History[i]; msg.Role == model.RoleUser && !strings.HasPrefix(msg.Content, ReminderPrefix) {
			return i
		}
	}
	return -1
}
