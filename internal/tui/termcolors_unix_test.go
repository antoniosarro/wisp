//go:build unix

package tui

import (
	"bytes"
	"errors"
	"image/color"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// colorAnswers is what a terminal replies to termColorQuery: foreground,
// background, one palette entry, then device attributes to end it.
const colorAnswers = "\x1b]10;rgb:ffff/eeee/dddd\x07\x1b]11;rgb:1111/2222/3333\x07" +
	"\x1b]4;5;rgb:ff00/0000/ff00\x07\x1b[?62;22c"

// fakeTerminal returns the tty side of a pseudo-terminal, as wisp's stdin,
// and answers on the other side once the query arrives on out (unless
// answer is empty).
func fakeTerminal(t *testing.T, answer string) (tty *os.File, out *bytes.Buffer, queried chan struct{}) {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })
	out, queried = &bytes.Buffer{}, make(chan struct{})
	if answer != "" {
		go func() {
			<-queried
			_, _ = ptmx.WriteString(answer)
		}()
	}
	return tty, out, queried
}

// queryWriter records writes to buf and signals queried once the color
// query is among them. Only Write: embedding the buffer would give it a
// WriteString that io.WriteString calls instead.
type queryWriter struct {
	buf     *bytes.Buffer
	queried chan struct{}
}

func (w queryWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), termColorQuery()) {
		select {
		case <-w.queried:
		default:
			close(w.queried)
		}
	}
	return n, err
}

func TestQueryTermColorsReadsTheAnswers(t *testing.T) {
	tty, out, queried := fakeTerminal(t, colorAnswers)
	got := queryTermColors(tty, queryWriter{out, queried}, 2*time.Second)
	if !strings.Contains(out.String(), termColorQuery()) {
		t.Fatalf("query not sent: %q", out.String())
	}
	if got.fg != (color.RGBA{0xff, 0xee, 0xdd, 0xff}) || got.bg != (color.RGBA{0x11, 0x22, 0x33, 0xff}) {
		t.Errorf("fg %v, bg %v", got.fg, got.bg)
	}
	if got.ansi[5] != (color.RGBA{0xff, 0x00, 0xff, 0xff}) {
		t.Errorf("ansi 5 = %v", got.ansi[5])
	}
}

// A terminal that never answers costs the timeout, not a hang.
func TestQueryTermColorsGivesUpOnSilence(t *testing.T) {
	tty, out, _ := fakeTerminal(t, "")
	start := time.Now()
	got := queryTermColors(tty, out, 100*time.Millisecond)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("waited %v for a silent terminal", took)
	}
	if got.fg != nil || got.bg != nil {
		t.Errorf("colors %+v from no answer", got)
	}
}

// Not a terminal: no query, no colors.
func TestQueryTermColorsNeedsATerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out bytes.Buffer
	if got := queryTermColors(f, &out, time.Second); got.fg != nil || out.Len() > 0 {
		t.Errorf("colors %+v, wrote %q", got, out.String())
	}
}

func TestKittyGraphicsSupported(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"TERM": "xterm-kitty"}, true},
		{map[string]string{"KITTY_WINDOW_ID": "1"}, true},
		{map[string]string{"TERM": "xterm-ghostty"}, true},
		{map[string]string{"TERM_PROGRAM": "ghostty"}, true},
		{map[string]string{"TERM": "xterm-256color"}, false},
		{map[string]string{"TERM": "xterm-kitty", "TMUX": "/tmp/tmux"}, false},
		{map[string]string{"TERM": "xterm-kitty", "WISP_LOGO": "text"}, false},
	} {
		for _, k := range []string{"TERM", "KITTY_WINDOW_ID", "TERM_PROGRAM", "TMUX", "WISP_LOGO"} {
			t.Setenv(k, c.env[k])
		}
		if got := kittyGraphicsSupported(); got != c.want {
			t.Errorf("%v: kittyGraphicsSupported() = %v, want %v", c.env, got, c.want)
		}
	}
}

// On a kitty terminal the logo and the mascot's frames are uploaded once,
// after which the splash draws the logo from placeholders.
func TestUploadImagesOnKitty(t *testing.T) {
	t.Setenv("TERM", "xterm-kitty")
	t.Setenv("TMUX", "")
	t.Setenv("WISP_LOGO", "")
	t.Cleanup(func() { logoImageReady, mascotImages = false, nil })
	tty, out, queried := fakeTerminal(t, colorAnswers)

	UploadImages(tty, queryWriter{out, queried})
	if !logoImageReady || len(mascotImages) == 0 {
		t.Fatalf("logoImageReady %v, %d mascot states uploaded", logoImageReady, len(mascotImages))
	}
	if n := strings.Count(out.String(), "\x1b_G"); n < 1+len(mascotImages) {
		t.Errorf("%d kitty graphics commands for the logo and %d mascot states", n, len(mascotImages))
	}
	if logo := renderLogoImage(); logo == "" || logo != renderPlaceholders(logoImageID, logoCols, logoRows) {
		t.Error("the splash logo isn't drawn from the uploaded image")
	}
}

// Elsewhere nothing is written: the text logo and mascot are used.
func TestUploadImagesElsewhere(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM_PROGRAM", "")
	var out bytes.Buffer
	UploadImages(nil, &out)
	if out.Len() > 0 || logoImageReady {
		t.Errorf("wrote %d bytes; logoImageReady %v", out.Len(), logoImageReady)
	}
}

// A deadline that passes while an answer is being read must end the wait:
// poll takes a negative timeout as "wait forever", which held startup
// until a key press when a terminal answered slowly and then went quiet.
func TestReadTermColorsNeverWaitsForever(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	if _, err := w.WriteString("\x1b]10;rgb:ffff/eeee/dddd\x07"); err != nil { // part of the answers
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Millisecond)
	var timeouts []int
	poll = func(fds []unix.PollFd, timeout int) (int, error) {
		timeouts = append(timeouts, timeout)
		if len(timeouts) == 1 {
			time.Sleep(time.Until(deadline) + 10*time.Millisecond) // the answer was slow
			return 1, nil
		}
		if timeout < 0 {
			return 0, errors.New("poll would wait forever")
		}
		return unix.Poll(fds, timeout)
	}
	t.Cleanup(func() { poll = unix.Poll })

	got := readTermColors(r, deadline)
	for _, n := range timeouts {
		if n < 0 {
			t.Fatalf("poll timeouts %v: a negative one waits forever", timeouts)
		}
	}
	if got.fg == nil {
		t.Error("the answer read before the deadline was dropped")
	}
}

// A signal interrupting the wait, such as a window resize, isn't the
// terminal's silence: the answers that follow are still read.
func TestReadTermColorsSurvivesInterruptedPoll(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	if _, err := w.WriteString(colorAnswers); err != nil {
		t.Fatal(err)
	}
	interrupted := false
	poll = func(fds []unix.PollFd, timeout int) (int, error) {
		if !interrupted {
			interrupted = true
			return -1, unix.EINTR
		}
		return unix.Poll(fds, timeout)
	}
	t.Cleanup(func() { poll = unix.Poll })

	if got := readTermColors(r, time.Now().Add(time.Second)); got.fg == nil || got.bg == nil {
		t.Errorf("colors = %+v after an interrupted poll, want the answers", got)
	}
}
