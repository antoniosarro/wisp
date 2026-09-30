//go:build unix

package tui

import (
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// queryTermColors asks the terminal for its colors and waits up to timeout
// for the answers. in is in raw mode meanwhile, so they arrive unbuffered
// and aren't echoed; anything typed during the wait is dropped.
func queryTermColors(in *os.File, out io.Writer, timeout time.Duration) termColors {
	fd := int(in.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return termColors{}
	}
	defer func() { _ = term.Restore(fd, state) }()
	if _, err := io.WriteString(out, termColorQuery()); err != nil {
		return termColors{}
	}
	return readTermColors(in, time.Now().Add(timeout))
}

// readTermColors reads the answers to termColorQuery from in until they
// are complete or deadline passes.
func readTermColors(in *os.File, deadline time.Time) termColors {
	var buf []byte
	p := make([]byte, 512)
	for {
		// A negative timeout would make poll wait for good: a terminal that
		// answers slowly, then stops, would hold startup until a key press.
		left := int(max(0, time.Until(deadline).Milliseconds()))
		fds := []unix.PollFd{{Fd: int32(in.Fd()), Events: unix.POLLIN}}
		n, err := poll(fds, left)
		if errors.Is(err, unix.EINTR) {
			continue // a signal, not the answer; the deadline still holds
		}
		if err != nil || n == 0 {
			break
		}
		n, err = in.Read(p)
		buf = append(buf, p[:max(n, 0)]...)
		if _, done := parseTermColors(string(buf)); done || err != nil {
			break
		}
	}
	t, _ := parseTermColors(string(buf))
	return t
}
