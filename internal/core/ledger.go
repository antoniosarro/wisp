package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/textfmt"
	"github.com/antoniosarro/wisp/internal/tokencount"
)

// Ledger holds the facts compaction carries over deterministically, taken
// from tool calls and user messages rather than recalled by the model: the
// model is worst at remembering which files it changed, and writing less
// makes the summary faster.
//
// A Ledger is folded from history with Update, so the ledger kept with one
// compaction, updated with the messages after its cut, is the ledger of
// the whole session. It encodes to JSON for storage.
type Ledger struct {
	Modified []FileEdit `json:",omitempty"` // least recently changed first
	Read     []FileRead `json:",omitempty"` // read and not changed since; least recent first
	Commands []Command  `json:",omitempty"` // least recent first
	Todo     []Todo     `json:",omitempty"` // the latest list
	User     []string   `json:",omitempty"` // user messages, each cut to maxUserBytes

	// pending holds calls whose results haven't been folded in yet; a
	// ledger is stored only between turns, when there are none.
	pending map[string]model.ToolCall
}

// FileEdit is a file changed by write, edit, or multi_edit calls that
// succeeded.
type FileEdit struct {
	Path  string
	Edits int
	// Check is the latest test or build command run since the last edit,
	// nil if none has.
	Check *Command `json:",omitempty"`
}

// FileRead is a file read and the line ranges read, merged.
type FileRead struct {
	Path  string
	Lines [][2]int `json:",omitempty"`
}

// Command is a bash command and how its latest run exited.
type Command struct {
	Command  string
	Exit     int // -1 when it failed without an exit code (a timeout)
	Runs     int
	Failures int `json:",omitempty"` // runs that exited nonzero, so a later pass doesn't hide them
}

// Todo is one task of the todo tool's list.
type Todo struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

const (
	maxUserBytes    = 1024 // each user message is cut to this
	maxUserTokens   = 8192 // user messages rendered at most
	maxLedgerFiles  = 30   // files listed per section, most recent kept
	maxLedgerCmds   = 20   // commands listed, most recent kept
	maxCommandBytes = 200
)

// checkCommand matches commands that test or build the code, whose exit
// status says whether an edit was verified.
var checkCommand = regexp.MustCompile(`\b(test|tests|build|check|vet|lint|clippy|pytest|jest|vitest|tsc|mypy|ruff|make|just|nix (build|flake check))\b`)

// Update folds msgs into the ledger: a tool call counts once its result
// arrives, and only if it succeeded. Harness reminders aren't the user's.
func (lg *Ledger) Update(msgs []model.Message) {
	if lg.pending == nil {
		lg.pending = map[string]model.ToolCall{}
	}
	for _, msg := range msgs {
		switch msg.Role {
		case model.RoleUser:
			if !strings.HasPrefix(msg.Content, ReminderPrefix) {
				lg.User = append(lg.User, textfmt.Cut(msg.Content, maxUserBytes))
			}
		case model.RoleAssistant:
			for _, call := range msg.ToolCalls {
				lg.pending[call.ID] = call
			}
		case model.RoleTool:
			call, ok := lg.pending[msg.ToolCallID]
			if ok {
				delete(lg.pending, msg.ToolCallID)
				lg.apply(call, msg)
			}
		}
	}
}

// apply folds one call and its result in. A bash command counts even when
// it failed, as a failure; anything else counts only if it succeeded.
func (lg *Ledger) apply(call model.ToolCall, result model.Message) {
	code, hasCode := exitCode(result.Content)
	switch {
	case call.Name == "bash":
		var a struct{ Command string }
		_ = json.Unmarshal(call.Args, &a)
		if a.Command == "" {
			return
		}
		cmd := Command{Command: textfmt.Cut(strings.Join(strings.Fields(a.Command), " "), maxCommandBytes), Exit: code, Runs: 1}
		if result.IsError && !hasCode {
			cmd.Exit = -1
		}
		if cmd.Exit != 0 {
			cmd.Failures = 1
		}
		if i := slices.IndexFunc(lg.Commands, func(c Command) bool { return c.Command == cmd.Command }); i >= 0 {
			cmd.Runs += lg.Commands[i].Runs
			cmd.Failures += lg.Commands[i].Failures
			lg.Commands = slices.Delete(lg.Commands, i, i+1)
		}
		lg.Commands = append(lg.Commands, cmd)
		if checkCommand.MatchString(a.Command) {
			for i := range lg.Modified {
				lg.Modified[i].Check = &cmd
			}
		}
	case result.IsError:
	case fileWriters[call.Name]:
		path, _ := readSpan(call)
		lg.Read = slices.DeleteFunc(lg.Read, func(r FileRead) bool { return r.Path == path })
		edit := FileEdit{Path: path, Edits: 1}
		if i := slices.IndexFunc(lg.Modified, func(f FileEdit) bool { return f.Path == path }); i >= 0 {
			edit.Edits += lg.Modified[i].Edits
			lg.Modified = slices.Delete(lg.Modified, i, i+1)
		}
		lg.Modified = append(lg.Modified, edit)
	case call.Name == "read":
		path, _ := readSpan(call)
		read := FileRead{Path: path}
		if i := slices.IndexFunc(lg.Read, func(r FileRead) bool { return r.Path == path }); i >= 0 {
			read = lg.Read[i]
			lg.Read = slices.Delete(lg.Read, i, i+1)
		}
		if first, last := numberedLines(result.Content); first > 0 {
			read.Lines = mergeRange(read.Lines, [2]int{first, last})
		}
		lg.Read = append(lg.Read, read)
	case call.Name == "todo":
		var a struct{ Todos []Todo }
		if json.Unmarshal(call.Args, &a) == nil {
			lg.Todo = a.Todos
		}
	}
}

// mergeRange adds r to sorted, disjoint ranges, joining the ones it
// overlaps or touches.
func mergeRange(ranges [][2]int, r [2]int) [][2]int {
	var out [][2]int
	for _, x := range ranges {
		if x[1]+1 < r[0] || r[1]+1 < x[0] {
			out = append(out, x)
			continue
		}
		r = [2]int{min(r[0], x[0]), max(r[1], x[1])}
	}
	out = append(out, r)
	slices.SortFunc(out, func(a, b [2]int) int { return a[0] - b[0] })
	return out
}

// Render writes the ledger as the tagged sections appended to a summary.
// User messages are kept newest first up to userTokens, then shown oldest
// first; a section with nothing in it is left out.
func (lg *Ledger) Render(userTokens int) string {
	var b strings.Builder
	section := func(tag string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "<%s>\n%s\n</%s>\n", tag, strings.Join(lines, "\n"), tag)
	}

	var lines []string
	for _, f := range recent(lg.Modified, maxLedgerFiles, &lines) {
		line := fmt.Sprintf("%s: %d edit%s", f.Path, f.Edits, plural(f.Edits))
		if f.Check != nil {
			line += fmt.Sprintf("; last check `%s` %s", f.Check.Command, exitText(f.Check.Exit))
		} else {
			line += "; not checked since"
		}
		lines = append(lines, line)
	}
	section("files-modified", lines)

	lines = nil
	for _, f := range recent(lg.Read, maxLedgerFiles, &lines) {
		ranges := make([]string, len(f.Lines))
		for i, r := range f.Lines {
			ranges[i] = fmt.Sprintf("%d-%d", r[0], r[1])
		}
		line := f.Path
		if len(ranges) > 0 {
			line += " lines " + strings.Join(ranges, ", ")
		}
		lines = append(lines, line)
	}
	section("files-read", lines)

	lines = nil
	for _, c := range recent(lg.Commands, maxLedgerCmds, &lines) {
		runs := ""
		switch {
		case c.Runs > 1 && c.Failures > 0 && c.Exit == 0:
			runs = fmt.Sprintf(" (%d runs, %d failed)", c.Runs, c.Failures)
		case c.Runs > 1:
			runs = fmt.Sprintf(" (%d runs)", c.Runs)
		}
		lines = append(lines, fmt.Sprintf("%s%s: %s", exitText(c.Exit), runs, c.Command))
	}
	section("commands", lines)

	lines = nil
	marks := map[string]string{"completed": "[x]", "in_progress": "[>]", "pending": "[ ]"}
	for _, t := range lg.Todo {
		lines = append(lines, marks[t.Status]+" "+t.Content)
	}
	section("todo", lines)

	lines = nil
	used, first := 0, len(lg.User)
	for first > 0 {
		n := tokencount.Count(lg.User[first-1])
		if used+n > userTokens {
			break
		}
		used += n
		first--
	}
	if first > 0 {
		lines = append(lines, fmt.Sprintf("(%d earlier message%s left out)", first, plural(first)))
	}
	for i, m := range lg.User[first:] {
		lines = append(lines, fmt.Sprintf("%d. %s", first+i+1, m))
	}
	section("user-messages", lines)

	return b.String()
}

// recent returns the last n items, noting in lines how many were left out.
func recent[T any](items []T, n int, lines *[]string) []T {
	if len(items) <= n {
		return items
	}
	*lines = append(*lines, fmt.Sprintf("(%d earlier left out)", len(items)-n))
	return items[len(items)-n:]
}

// exitText describes a command's exit, -1 meaning it had none.
func exitText(code int) string {
	if code < 0 {
		return "failed"
	}
	return fmt.Sprintf("exit %d", code)
}

// plural is the "s" of a count other than one.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
