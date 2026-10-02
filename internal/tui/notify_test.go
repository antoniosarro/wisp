package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/assets"
	"github.com/antoniosarro/wisp/internal/testutil"
)

// notifications runs cmd, if any, and returns what it showed: desktop
// notifications as "title | body", and "BEL" for each bell.
func notifications(t *testing.T, desktopErr error, cmd tea.Cmd) []string {
	t.Helper()
	var shown []string
	origNotify, origOut := desktopNotify, terminalOut
	defer func() { desktopNotify, terminalOut = origNotify, origOut }()
	desktopNotify = func(_ context.Context, title, body string) error {
		if desktopErr == nil {
			shown = append(shown, title+" | "+body)
		}
		return desktopErr
	}
	var out bytes.Buffer
	terminalOut = &out
	if cmd != nil {
		cmd()
	}
	for range strings.Count(out.String(), "\a") {
		shown = append(shown, "BEL")
	}
	return shown
}

func TestNotifications(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.opts.WorkDir = "/home/me/project"
	bash := PermissionRequestMsg{Name: "bash", Args: json.RawMessage(`{"command":"go test ./...\necho done"}`), Reply: make(chan Answer, 1)}

	if got := notifications(t, nil, m.notifyApproval(bash)); len(got) != 0 {
		t.Errorf("--notify off: %v", got)
	}

	m.opts.Notify = NotifyDesktop
	if got := notifications(t, nil, m.notifyApproval(bash)); len(got) != 1 || got[0] != "wisp needs your approval · project | wisp wants to use bash: go test ./..." {
		t.Errorf("approval: %v", got)
	}
	agent := bash
	agent.Agent = "explorer"
	if got := notifications(t, nil, m.notifyApproval(agent)); len(got) != 1 || !strings.Contains(got[0], "| explorer wants to use bash") {
		t.Errorf("a sub-agent's approval: %v", got)
	}
	if got := notifications(t, errors.New("no notify-send"), m.notifyApproval(bash)); len(got) != 1 || got[0] != "BEL" {
		t.Errorf("without a notification service: %v, want the bell", got)
	}

	// Turn ends: a short one is watched; cancelling means the user is there.
	m.turnStarted = time.Now()
	if got := notifications(t, nil, m.notifyTurnDone(TurnDoneMsg{Answer: "quick"})); len(got) != 0 {
		t.Errorf("a short turn: %v", got)
	}
	m.turnStarted = time.Now().Add(-time.Minute)
	if got := notifications(t, nil, m.notifyTurnDone(TurnDoneMsg{Answer: "\nAll 42 tests pass.\nDetails..."})); len(got) != 1 || got[0] != "wisp finished · project | All 42 tests pass." {
		t.Errorf("a long turn: %v", got)
	}
	if got := notifications(t, nil, m.notifyTurnDone(TurnDoneMsg{Err: errors.New("starting stream: 402 Payment Required")})); len(got) != 1 || !strings.HasPrefix(got[0], "wisp stopped · project | starting stream: 402") {
		t.Errorf("a failed turn: %v", got)
	}
	for _, msg := range []TurnDoneMsg{{Err: context.Canceled}, {Compact: true}} {
		if got := notifications(t, nil, m.notifyTurnDone(msg)); len(got) != 0 {
			t.Errorf("%+v: %v", msg, got)
		}
	}

	m.opts.Notify = NotifyBell
	if got := notifications(t, nil, m.notifyApproval(bash)); len(got) != 1 || got[0] != "BEL" {
		t.Errorf("--notify bell: %v", got)
	}

	// Looking at the terminal: nothing, until it loses focus again.
	m.Update(tea.FocusMsg{})
	if got := notifications(t, nil, m.notifyApproval(bash)); len(got) != 0 {
		t.Errorf("focused: %v", got)
	}
	m.Update(tea.BlurMsg{})
	if got := notifications(t, nil, m.notifyApproval(bash)); len(got) != 1 {
		t.Errorf("unfocused again: %v", got)
	}
}

// Only the request that starts the waiting notifies: the ones queued
// behind it are seen with it.
func TestApprovalNotifiesOnce(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.opts.Notify = NotifyBell
	var cmds []tea.Cmd
	for range 3 {
		m.handleTurnMsg(PermissionRequestMsg{Name: "bash", Args: json.RawMessage(`{"command":"ls"}`), Reply: make(chan Answer, 1)})
		cmds = append(cmds, m.notification)
		m.notification = nil
	}
	if cmds[0] == nil || cmds[1] != nil || cmds[2] != nil {
		t.Errorf("notifications for 3 queued requests: %v, want only the first", cmds)
	}
}

// The icon is the logo's mark on a dark rounded tile, written once to the
// cache directory for notify-send.
func TestNotifyIcon(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := notifyIconPath()
	data, err := os.ReadFile(path)
	if err != nil || filepath.Base(filepath.Dir(path)) != "wisp" {
		t.Fatalf("icon at %q: %v", path, err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != notifyIconSize || b.Dy() != notifyIconSize {
		t.Errorf("icon is %v, want %d px square", b, notifyIconSize)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Error("the corner is not transparent: the tile isn't rounded")
	}
	if _, _, _, a := img.At(notifyIconSize/2, notifyIconSize/2).RGBA(); a != 0xffff {
		t.Error("the middle is not opaque")
	}

	logo, _ := png.Decode(bytes.NewReader(assets.Logo))
	mark := logoMark(logo)
	if mark.Empty() || mark.Max.Y > logo.Bounds().Dy()*7/10 {
		t.Errorf("mark %v of a %v logo: want the ghost without the word below it", mark, logo.Bounds())
	}
}
