//go:build !linux

package builtin

import "os/exec"

// Linux is the supported platform. Elsewhere, cancelling kills only the
// shell, and WaitDelay still bounds how long its children can hold the
// output open.
func configureProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup has no group to kill outside Linux.
func killProcessGroup(cmd *exec.Cmd) bool { return false }
