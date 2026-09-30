//go:build linux || darwin

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"

	"github.com/antoniosarro/wisp/internal/testutil/fakemodel"
)

// terminal is the real wisp binary running in a pseudo-terminal, with its
// output fed to a headless terminal emulator whose screen tests can read.
type terminal struct {
	t      *testing.T
	pty    *os.File
	screen vt10x.Terminal
	cmd    *exec.Cmd
	exited chan error
}

// startTerminal runs wisp with args in a cols×rows terminal, in a fresh
// working directory, against the fake model server at baseURL.
func startTerminal(t *testing.T, baseURL string, cols, rows int, args ...string) *terminal {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// TestMain runs wisp when the binary is invoked under that name.
	bin := filepath.Join(t.TempDir(), "wisp")
	if err := os.Symlink(exe, bin); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(bin, append([]string{"--base-url", baseURL}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_STATE_HOME=" + filepath.Join(home, "state"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		"XDG_DATA_HOME=" + filepath.Join(home, "data"),
		"TERM=xterm-256color",
		"WISP_LOGO=text",
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	term := &terminal{t: t, pty: f, screen: vt10x.New(vt10x.WithSize(cols, rows)), cmd: cmd, exited: make(chan error, 1)}
	go term.feed()
	go func() { term.exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = f.Close()
	})
	return term
}

// feed copies wisp's output to the emulator. Not io.Copy: vt10x reports a
// character split across reads as a short write, which would stop the
// copy; the unwritten bytes are kept for the next read instead. It also
// answers the queries a real terminal answers and vt10x doesn't:
// bubbletea asks for the background colour (then the cursor position)
// when it starts, and waits up to 5 seconds for a reply.
func (term *terminal) feed() {
	buf := make([]byte, 32<<10)
	var pending []byte
	for {
		n, err := term.pty.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if bytes.Contains(chunk, []byte("\x1b]11;?")) {
				_, _ = term.pty.WriteString("\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\")
			}
			if bytes.Contains(chunk, []byte("\x1b[6n")) {
				_, _ = term.pty.WriteString("\x1b[1;1R")
			}
			pending = append(pending, chunk...)
			w, _ := term.screen.Write(pending)
			pending = append(pending[:0], pending[w:]...)
		}
		if err != nil {
			return
		}
	}
}

// waitFor waits until the screen shows want, failing with the screen.
func (term *terminal) waitFor(want string) {
	term.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(term.screen.String(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	term.t.Fatalf("screen never showed %q:\n%s", want, term.screen.String())
}

func (term *terminal) typeKeys(s string) {
	term.t.Helper()
	if _, err := term.pty.WriteString(s); err != nil {
		term.t.Fatal(err)
	}
}

// TestTUIInTerminal drives the real TUI the way a user does: it waits for
// the prompt, sends a message, reads the streamed answer off the screen,
// and exits with Ctrl+C twice.
func TestTUIInTerminal(t *testing.T) {
	srv := fakemodel.Start(fakemodel.Script{
		Models: []map[string]any{{"id": "fake", "max_model_len": 32768}},
		Rules: []fakemodel.Rule{
			{When: fakemodel.When{Contains: "hello"}, Reply: fakemodel.Reply{Reasoning: "a short thought", Text: "Hello from the terminal test.", Chunk: 5}},
		},
	}, "")
	defer srv.Close()

	term := startTerminal(t, srv.URL, 100, 30)
	term.waitFor("ask wisp")
	term.waitFor("fake") // the splash names the model chosen from the endpoint

	term.typeKeys("hello there")
	term.waitFor("hello there")
	term.typeKeys("\r")
	term.waitFor("Hello from the terminal test.")
	term.waitFor("Thought for 3 words")

	screen := term.screen.String()
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	if len(lines) > 30 || !strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "╰") {
		t.Errorf("the input box is not at the bottom of the screen:\n%s", screen)
	}

	// The turn may still be winding down ("Cancelling · Ctrl+C again to
	// exit") or over ("Press Ctrl+C again to exit"): either arms quitting.
	term.typeKeys("\x03")
	term.waitFor("Ctrl+C again to exit")
	term.typeKeys("\x03")
	select {
	case err := <-term.exited:
		if err != nil {
			t.Fatalf("wisp exited with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("wisp did not exit after Ctrl+C twice:\n%s", term.screen.String())
	}
}
