package tui

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Notify modes, from --notify: how wisp tells the user it needs them or
// is done while they look elsewhere.
const (
	NotifyOff     = "off"
	NotifyBell    = "bell"    // the terminal bell: a flash, a sound, or an urgency hint
	NotifyDesktop = "desktop" // a desktop notification, the bell where there is none
)

// notifyFinishedAfter is how long a turn must run for its end to notify:
// after a shorter one the user is most likely still watching.
const notifyFinishedAfter = 10 * time.Second

// notifyTimeout bounds a desktop notification's command.
const notifyTimeout = 5 * time.Second

// desktopNotify is how a desktop notification is shown (notify_linux.go); a
// variable so tests can see what would be sent.
var desktopNotify = showDesktopNotification

// notifyUser tells the user title and body the way opts.Notify says, unless
// the terminal reported they are looking at it. It never blocks the UI.
func (m *Model) notifyUser(title, body string) tea.Cmd {
	mode := m.opts.Notify
	if mode == "" || mode == NotifyOff || m.focused {
		return nil
	}
	if dir := filepath.Base(m.opts.WorkDir); dir != "." && dir != "/" && dir != "" {
		title += " · " + dir // which wisp, with several open
	}
	return func() tea.Msg {
		if mode == NotifyDesktop {
			ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			defer cancel()
			if desktopNotify(ctx, title, body) == nil {
				return nil
			}
		}
		_, _ = io.WriteString(terminalOut, "\a")
		return nil
	}
}

// notifyApproval announces a request that starts waiting.
func (m *Model) notifyApproval(req PermissionRequestMsg) tea.Cmd {
	who := "wisp"
	if req.Agent != "" {
		who = req.Agent
	}
	return m.notifyUser("wisp needs your approval", who+" wants to use "+req.Name+callDetail(req.Args))
}

// notifyTurnDone announces the end of a turn that ran long enough, or
// failed; not one the user cancelled, since they are there.
func (m *Model) notifyTurnDone(msg TurnDoneMsg) tea.Cmd {
	switch {
	case msg.Compact || errors.Is(msg.Err, context.Canceled):
		return nil
	case msg.Err != nil:
		return m.notifyUser("wisp stopped", firstLine(msg.Err.Error(), 160))
	case time.Since(m.turnStarted) < notifyFinishedAfter:
		return nil
	}
	return m.notifyUser("wisp finished", cmp.Or(firstLine(msg.Answer, 160), "The turn is done."))
}

// callDetail is what a call acts on, for a notification: ": " and the
// command, path, URL, or query, on one line.
func callDetail(args json.RawMessage) string {
	var a struct{ Command, Path, URL, Query string }
	_ = json.Unmarshal(args, &a)
	for _, s := range []string{a.Command, a.Path, a.URL, a.Query} {
		if s != "" {
			return ": " + firstLine(s, 100)
		}
	}
	return ""
}

// firstLine is s's first non-empty line, cut to at most n runes.
func firstLine(s string, n int) string {
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if r := []rune(line); len(r) > n {
				return string(r[:n]) + "…"
			}
			return line
		}
	}
	return ""
}
