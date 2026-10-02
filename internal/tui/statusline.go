package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// statusline is the one dim row under the input: model and reasoning
// effort, directory, git branch, context fill and session spend. Segments
// that don't apply are left out, and the row is truncated to the terminal
// width. It is empty on short terminals.
func (m *Model) statusline() string {
	if m.height < 12 {
		return "" // no room to spare from the chat box
	}
	parts := []string{sanitize(m.opts.Model.ID)}
	if e := m.loop.Effort; e != "" {
		parts[0] += " (" + sanitize(e) + ")"
	}
	if dir := sanitize(tildePath(m.opts.WorkDir)); dir != "" {
		parts = append(parts, dir)
	}
	if b := gitBranch(m.opts.WorkDir); b != "" {
		parts = append(parts, "⎇ "+sanitize(b))
	}
	if c := m.stats.Context; c.Window > 0 && c.History > 0 {
		parts = append(parts, fmt.Sprintf("ctx %.0f%%", c.UsedPct()))
	}
	if c := m.costSummary(); c.Session+c.Agents > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", c.Session+c.Agents))
	}
	line := " " + strings.Join(parts, " · ")
	return styleDim.Render(ansi.Truncate(line, max(1, m.width), "…"))
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "~"
			}
			return "~/" + rel
		}
	}
	return p
}

// gitBranch reads the checked-out branch from .git/HEAD in dir or a parent;
// a detached HEAD gives its short hash. Worktrees (.git as a file) show none.
func gitBranch(dir string) string {
	for ; dir != "" && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		b, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
		if err != nil {
			continue
		}
		head := strings.TrimSpace(string(b))
		if ref, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
			return ref
		}
		return head[:min(7, len(head))]
	}
	return ""
}

// statuslineRow is the statusline with its newline, or nothing.
func (m *Model) statuslineRow() string {
	if s := m.statusline(); s != "" {
		return "\n" + s
	}
	return ""
}
