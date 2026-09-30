//go:build unix

package tui

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// pipeInput is an Input reading a pipe the test writes to.
func pipeInput(t *testing.T) (*Input, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return NewInput(r), w
}

func TestInputJoinsSplitSequence(t *testing.T) {
	in, w := pipeInput(t)
	if _, err := w.WriteString("\x1b[<35;12"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(sequenceWait / 4)
		_, _ = w.WriteString(";5M")
	}()

	buf := make([]byte, 64)
	n, err := in.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "\x1b[<35;12;5M" {
		t.Errorf("Read = %q, want the whole mouse report", got)
	}
}

func TestInputReturnsLoneEsc(t *testing.T) {
	in, w := pipeInput(t)
	if _, err := w.WriteString("\x1b"); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 64)
	start := time.Now()
	n, err := in.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "\x1b" {
		t.Errorf("Read = %q, want the Esc alone", got)
	}
	if took := time.Since(start); took > 10*sequenceWait {
		t.Errorf("a lone Esc took %v, want about %v", took, sequenceWait)
	}
}

// A signal (a window resize) interrupting the wait must not end it:
// poll then returns EINTR, and the sequence would reach Bubble Tea split.
func TestInputWaitSurvivesInterruptedPoll(t *testing.T) {
	interrupted := false
	poll = func(fds []unix.PollFd, timeout int) (int, error) {
		if !interrupted {
			interrupted = true
			return -1, unix.EINTR
		}
		return unix.Poll(fds, timeout)
	}
	t.Cleanup(func() { poll = unix.Poll })

	in, w := pipeInput(t)
	if _, err := w.WriteString("\x1b[<35;12"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(sequenceWait / 4)
		_, _ = w.WriteString(";5M")
	}()

	buf := make([]byte, 64)
	n, err := in.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "\x1b[<35;12;5M" {
		t.Errorf("Read = %q, want the whole mouse report despite EINTR", got)
	}
	if !interrupted {
		t.Error("the fake poll was never called")
	}
}
