//go:build linux || darwin

package tui

import (
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
)

// ptyRecorder records keys, pastes, and mouse events as strings.
type ptyRecorder struct{ got []string }

func (r *ptyRecorder) Init() tea.Cmd { return nil }
func (r *ptyRecorder) View() string  { return "" }
func (r *ptyRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Paste {
			r.got = append(r.got, "paste "+string(msg.Runes))
		} else {
			r.got = append(r.got, "key "+msg.String())
		}
	case tea.MouseMsg:
		r.got = append(r.got, fmt.Sprintf("mouse %s %d,%d", tea.MouseEvent(msg), msg.X, msg.Y))
	}
	return r, nil
}

// typeInPTY runs a program reading a real pseudo-terminal through
// NewInput, as wisp does, and writes each chunk to the terminal gap apart,
// as a terminal delivers input in pieces.
func typeInPTY(t *testing.T, gap time.Duration, chunks ...string) []string {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	defer func() { _ = ptmx.Close(); _ = tty.Close() }()
	go func() { _, _ = io.Copy(io.Discard, ptmx) }() // drain what the program writes back

	rec := &ptyRecorder{}
	p := tea.NewProgram(rec, tea.WithInput(NewInput(tty)), tea.WithOutput(io.Discard))
	go func() {
		time.Sleep(50 * time.Millisecond) // raw mode is set once the program starts
		for _, c := range chunks {
			_, _ = ptmx.WriteString(c)
			time.Sleep(gap)
		}
		time.Sleep(150 * time.Millisecond)
		p.Quit()
	}()
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	return rec.got
}

func TestPTYInput(t *testing.T) {
	cases := []struct {
		name   string
		gap    time.Duration
		chunks []string
		want   []string
	}{
		{
			// Pieces must arrive within NewInput's sequenceWait of
			// each other; few gaps keep a loaded machine from stretching
			// one past it.
			name:   "mouse report split in three reads",
			gap:    3 * time.Millisecond,
			chunks: []string{"\x1b", "[<35;1", "2;5M"},
			want:   []string{"mouse motion 11,4"},
		},
		{
			name:   "two mouse reports in one read",
			chunks: []string{"\x1b[<0;3;4M\x1b[<0;3;4m"},
			want:   []string{"mouse left press 2,3", "mouse left release 2,3"},
		},
		{
			name:   "esc alone, then a key later, stays two keys",
			gap:    60 * time.Millisecond,
			chunks: []string{"\x1b", "a"},
			want:   []string{"key esc", "key a"},
		},
		{
			name:   "esc and a key together are alt+key",
			chunks: []string{"\x1ba"},
			want:   []string{"key alt+a"},
		},
		{
			name:   "alt+enter inserts a newline in the composer",
			chunks: []string{"\x1b\r"},
			want:   []string{"key alt+enter"},
		},
		{
			name:   "ctrl+j",
			chunks: []string{"\n"},
			want:   []string{"key ctrl+j"},
		},
		{
			name:   "bracketed paste split across reads",
			gap:    5 * time.Millisecond,
			chunks: []string{"\x1b[200~hello ", "wörld\x1b[201~"},
			want:   []string{"paste hello wörld"},
		},
		{
			name:   "arrow key split after the escape",
			gap:    5 * time.Millisecond,
			chunks: []string{"\x1b", "[A"},
			want:   []string{"key up"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := typeInPTY(t, c.gap, c.chunks...); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
