package tui

import (
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// termColors are the terminal's own colors, from its answers to OSC 10
// (foreground), OSC 11 (background) and OSC 4 (ANSI colors); nil where it
// didn't answer.
type termColors struct {
	fg, bg color.Color
	ansi   [16]color.Color
}

// mascotANSI maps the mascot's palette entries to the ANSI color replacing
// each: dim, magenta, cyan, lavender, mint, rose, amber. Entries 0–2
// (outline, body, body shade) follow the background and foreground instead.
// The order is assets/mascot/wisp.gpl's, where an entry's index is its role;
// a part that should recolor on its own needs a new entry appended after 10.
var mascotANSI = [...]int{3: 8, 4: 5, 5: 6, 6: 4, 7: 2, 8: 1, 9: 3}

// termColorQuery asks for the colors the mascot uses, then for primary
// device attributes: every terminal answers that, so its reply marks the
// end of the answers.
func termColorQuery() string {
	var b strings.Builder
	b.WriteString(ansi.RequestForegroundColor + ansi.RequestBackgroundColor)
	for _, n := range mascotANSI[3:] {
		fmt.Fprintf(&b, "\x1b]4;%d;?\x07", n)
	}
	b.WriteString(ansi.RequestPrimaryDeviceAttributes)
	return b.String()
}

// parseTermColors reads the answers to termColorQuery from s. done reports
// whether the device attributes reply, which comes last, has arrived.
func parseTermColors(s string) (t termColors, done bool) {
	for rest := s; ; {
		i := strings.Index(rest, "\x1b]")
		if i < 0 {
			break
		}
		rest = rest[i+2:]
		end := strings.IndexAny(rest, "\x07\x1b")
		if end < 0 {
			break
		}
		fields := strings.Split(rest[:end], ";")
		rest = rest[end:]
		switch {
		case len(fields) == 2 && fields[0] == "10":
			t.fg = ansi.XParseColor(fields[1])
		case len(fields) == 2 && fields[0] == "11":
			t.bg = ansi.XParseColor(fields[1])
		case len(fields) == 3 && fields[0] == "4":
			if n, err := strconv.Atoi(fields[1]); err == nil && n >= 0 && n < len(t.ansi) {
				t.ansi[n] = ansi.XParseColor(fields[2])
			}
		}
	}
	if i := strings.Index(s, "\x1b[?"); i >= 0 {
		done = strings.Contains(s[i:], "c")
	}
	return t, done
}

// mascotPalette returns p with each entry the terminal answered for
// replaced: the body in the foreground, the outline in the background, the
// shade between them, and the accents in the matching ANSI colors.
func (t termColors) mascotPalette(p color.Palette) color.Palette {
	out := slices.Clone(p)
	set := func(i int, c color.Color) {
		if c != nil && i < len(out) {
			out[i] = c
		}
	}
	set(0, t.bg)
	set(1, t.fg)
	if t.fg != nil && t.bg != nil {
		m := lerpColor(rgb(t.bg), rgb(t.fg), 0.7)
		set(2, color.RGBA{uint8(m[0]), uint8(m[1]), uint8(m[2]), 0xff})
	}
	for i := 3; i < len(mascotANSI); i++ {
		set(i, t.ansi[mascotANSI[i]])
	}
	return out
}

// rgb is c as an 8-bit RGB triple.
func rgb(c color.Color) [3]int {
	r, g, b, _ := c.RGBA()
	return [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
}
