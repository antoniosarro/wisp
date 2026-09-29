package builtin

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcessGroup starts cmd in a process group of its own, and
// makes cancelling it kill the whole group, so children the command
// started don't outlive a timeout.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
}

// killProcessGroup stops what the command left running in its group, e.g.
// a server started with &, and reports whether anything was.
func killProcessGroup(cmd *exec.Cmd) bool {
	return cmd.Process != nil && syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) == nil
}
