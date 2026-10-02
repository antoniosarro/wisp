//go:build !linux

package tui

import (
	"context"
	"errors"
)

// Linux is the supported platform; elsewhere the bell stands in for now.
func showDesktopNotification(context.Context, string, string) error {
	return errors.New("desktop notifications are only supported on Linux")
}
