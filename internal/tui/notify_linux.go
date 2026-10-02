package tui

import (
	"context"
	"os/exec"
)

// showDesktopNotification asks the desktop's notification service through
// notify-send (libnotify), which every Linux desktop understands, with
// wisp's icon. The text goes as arguments, never through a shell, and
// unescaped: services that render markup show text that doesn't parse as
// it is, and some (hyprpanel) escape it themselves, so escaping here would
// show "&amp;" and the like.
func showDesktopNotification(ctx context.Context, title, body string) error {
	args := []string{"--app-name=wisp"}
	if icon := notifyIconPath(); icon != "" {
		args = append(args, "--icon="+icon)
	}
	return exec.CommandContext(ctx, "notify-send", append(args, "--", title, body)...).Run()
}
