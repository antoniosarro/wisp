//go:build unix

package tui

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// sequenceWait is how long a read that ends inside an escape sequence waits
// for the rest. A lone Esc key press arrives as a single byte that looks
// identical, so this also bounds how long Esc can be delayed; it must still
// cover the gap a terminal or SSH link leaves when it splits a mouse report
// across reads, which is why it is not shorter.
const sequenceWait = 60 * time.Millisecond

// NewInput wraps terminal input so escape sequences are not split across
// reads. bubbletea v1 parses each read on its own and turns a split mouse
// report into an Esc key plus typed text such as "[<35;12;5M". It stays an
// *os.File, so bubbletea still sets raw mode and cancels reads.
func NewInput(f *os.File) *Input { return &Input{f} }

// Input is terminal input that holds back a read ending inside an escape
// sequence until the rest arrives.
type Input struct{ *os.File }

// Read reads from the terminal, completing an escape sequence split across
// reads when the rest follows within sequenceWait.
func (in *Input) Read(p []byte) (int, error) {
	n, err := in.File.Read(p)
	if err != nil || n == 0 {
		return n, err
	}
	buf := p[:n]
	// Wait for the remainder of a sequence split across reads. If nothing
	// arrives within sequenceWait this was a lone Esc key press, which is
	// returned rather than held back.
	for incompleteSequence(buf) && len(buf) < len(p) && in.readable(sequenceWait) {
		more, readErr := in.File.Read(p[len(buf):])
		buf = p[:len(buf)+more]
		if readErr != nil {
			break
		}
	}
	return len(buf), nil
}

// poll is unix.Poll, replaced in tests.
var poll = unix.Poll

// readable waits up to timeout for more input. A signal interrupts poll
// whatever SA_RESTART says, and Bubble Tea handles SIGWINCH: resizing the
// window mid-sequence must not end the wait early and split the sequence.
func (in *Input) readable(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		fds := []unix.PollFd{{Fd: int32(in.Fd()), Events: unix.POLLIN}}
		n, err := poll(fds, int(max(0, time.Until(deadline).Milliseconds())))
		if !errors.Is(err, unix.EINTR) {
			return err == nil && n > 0
		}
	}
}
