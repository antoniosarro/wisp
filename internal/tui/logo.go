package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/assets"
)

// On kitty-graphics terminals the splash shows assets/logo.png via Unicode
// placeholders: uploaded once, then referenced by ordinary text cells so it
// scrolls like text.
const (
	logoImageID = 87 // carried in the placeholder cells' 256-color foreground
	logoCols    = 14
	logoRows    = 10
	placeholder = '\U0010EEEE'
)

// logoImageReady is set once UploadImages has sent the image.
var logoImageReady bool

// placeholderDiacritics[i] marks a placeholder cell as row (or column) i.
var placeholderDiacritics = []rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F,
	0x0346, 0x034A, 0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357,
}

// kittyGraphicsSupported says whether the terminal can show images through
// Unicode placeholders: kitty and Ghostty, outside tmux. WISP_LOGO=text
// turns the images off.
func kittyGraphicsSupported() bool {
	if os.Getenv("WISP_LOGO") == "text" {
		return false
	}
	if os.Getenv("TMUX") != "" {
		return false // would need DCS passthrough
	}
	term := os.Getenv("TERM")
	return os.Getenv("KITTY_WINDOW_ID") != "" || term == "xterm-kitty" ||
		term == "xterm-ghostty" || os.Getenv("TERM_PROGRAM") == "ghostty"
}

// UploadImages switches to the alternate screen and uploads the logo and
// the mascot's frames when the terminal supports kitty graphics, with the
// mascot recolored in the terminal's colors read from in. Image storage is
// per screen, so this runs before Bubble Tea starts. Placements are created
// with the uploads because a screen clear frees images without one.
func UploadImages(in *os.File, w io.Writer) {
	if !kittyGraphicsSupported() {
		return
	}
	data, err := logoPNG()
	if err != nil {
		return
	}
	var b strings.Builder
	b.WriteString("\x1b[?1049h")
	writeKittyImage(&b, logoImageID, logoCols, logoRows, data)
	mascot := uploadMascot(&b, queryTermColors(in, w, 300*time.Millisecond))
	if _, err := io.WriteString(w, b.String()); err == nil {
		logoImageReady = true
		mascotImages = mascot
	}
}

// writeKittyImage transmits a PNG as image id with a virtual placement of
// cols×rows cells, for Unicode placeholders to show.
func writeKittyImage(b *strings.Builder, id, cols, rows int, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for first := true; enc != ""; first = false {
		n := min(len(enc), 4096)
		more := 0
		if n < len(enc) {
			more = 1
		}
		b.WriteString("\x1b_G")
		if first {
			fmt.Fprintf(b, "a=T,U=1,f=100,q=2,i=%d,c=%d,r=%d,", id, cols, rows)
		}
		fmt.Fprintf(b, "m=%d;%s\x1b\\", more, enc[:n])
		enc = enc[n:]
	}
}

// logoPNG crops the logo to its opaque area and, on dark themes, recolors
// its near-black pixels with the text logo's gradient.
func logoPNG() ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(assets.Logo))
	if err != nil {
		return nil, err
	}
	var bounds image.Rectangle
	sb := src.Bounds()
	for y := sb.Min.Y; y < sb.Max.Y; y++ {
		for x := sb.Min.X; x < sb.Max.X; x++ {
			if _, _, _, a := src.At(x, y).RGBA(); a > 0x8000 { // skip faint alpha noise
				bounds = bounds.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := range bounds.Dy() {
		for x := range bounds.Dx() {
			c := color.NRGBAModel.Convert(src.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			if !lightTheme && max(c.R, c.G, c.B)-min(c.R, c.G, c.B) < 48 {
				g := lerpColor(logoGradientFrom, logoGradientTo, float64(x)/float64(max(bounds.Dx()-1, 1)))
				c.R, c.G, c.B = uint8(g[0]), uint8(g[1]), uint8(g[2])
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	err = png.Encode(&buf, img)
	return buf.Bytes(), err
}

// renderLogoImage lays out the placeholder cells for the uploaded logo.
func renderLogoImage() string {
	return renderPlaceholders(logoImageID, logoCols, logoRows)
}

// renderPlaceholders lays out the cells that show an uploaded image. The
// image id is carried in the cells' 256-color foreground.
func renderPlaceholders(id, cols, rows int) string {
	var b strings.Builder
	for row := range rows {
		if row > 0 {
			b.WriteByte('\n')
		}
		// Later cells in the row inherit the row and advance the column.
		fmt.Fprintf(&b, "\x1b[38;5;%dm%c%c%c", id, placeholder, placeholderDiacritics[row], placeholderDiacritics[0])
		b.WriteString(strings.Repeat(string(placeholder), cols-1))
		b.WriteString("\x1b[39m")
	}
	return b.String()
}

// maxSplashPath keeps a long directory from pushing the logo off the splash.
const maxSplashPath = 60

// wispLogo is the text fallback logo, sized to the image's cell box.
const wispLogo = ` ▄██████▄
██  ██  ██
████╭─────╮
████│ >_  │ ━━
█▀█▀╰─────╯ ━
     ▝▘
█ ▄ █▐▌▄▀▀▐▛▚▖
▀▄▀▄▀▐▌▄▄▀▐▛▘`

// The text logo's gradient runs from magenta to cyan.
var (
	logoGradientFrom = [3]int{215, 65, 245}
	logoGradientTo   = [3]int{80, 220, 235}
)

// gradientLogo renders wispLogo once with the logo's color gradient.
var gradientLogo = sync.OnceValue(func() string { return gradientText(wispLogo) })

// gradientText colors s column by column from the logo's start to end color.
func gradientText(s string) string {
	lines := strings.Split(s, "\n")
	width := 0
	for _, line := range lines {
		width = max(width, utf8.RuneCountInString(line))
	}
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		for col, ch := range []rune(line) {
			c := lerpColor(logoGradientFrom, logoGradientTo, float64(col)/float64(max(width-1, 1)))
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(hexColor(c))).Render(string(ch)))
		}
	}
	return b.String()
}

// lerpColor is the color a fraction t of the way from from to to.
func lerpColor(from, to [3]int, t float64) [3]int {
	var c [3]int
	for i := range c {
		c[i] = from[i] + int(float64(to[i]-from[i])*t)
	}
	return c
}

// hexColor formats an RGB triple as #rrggbb.
func hexColor(c [3]int) string {
	clamp := func(v int) int { return min(255, max(0, v)) }
	return fmt.Sprintf("#%02x%02x%02x", clamp(c[0]), clamp(c[1]), clamp(c[2]))
}

// renderSplash is the top of the main chat: the logo beside the model,
// directory, session, and endpoint.
func renderSplash(opts Options, sessionID string, width int) string {
	logo := gradientLogo()
	if logoImageReady {
		logo = renderLogoImage()
	}

	var info strings.Builder
	// The model's name and details come from the endpoint.
	current := sanitize(opts.Model.ID) + "\n  " + styleDim.Render(sanitize(opts.Model.Summary()))
	if opts.Model.ID == "" {
		current = styleDim.Render("none yet · choose with /model")
	}
	var rows [][2]string
	if opts.Update != "" {
		// The tag comes from GitHub, by way of the state file.
		rows = append(rows, [2]string{"Update available", "Version " + sanitize(opts.Update)})
	}
	rows = append(rows, [2]string{"Model", current})
	if opts.WorkDir != "" {
		rows = append(rows, [2]string{"Directory", displayPath(showControls(opts.WorkDir), maxSplashPath)})
	}
	rows = append(rows, [2]string{"Session", styleDim.Render(sessionID)}, [2]string{"Endpoint", styleDim.Render(sanitize(opts.BaseURL))})
	if opts.TraceURL != "" {
		rows = append(rows, [2]string{"Trace", styleDim.Render(opts.TraceURL)})
	}
	for _, row := range rows {
		fmt.Fprintf(&info, "%s\n● %s\n\n", styleSplashHead.Render(row[0]), row[1])
	}
	info.WriteString(styleDim.Render("F1 or /help for controls"))

	splash := lipgloss.JoinHorizontal(lipgloss.Center, lipgloss.NewStyle().MarginLeft(2).Render(logo), "   ", info.String())
	if lipgloss.Width(splash) > width {
		splash = gradientText("wisp") + "\n" + info.String()
	}
	rule := ""
	if width > 0 {
		rule = styleRule.Render(strings.Repeat("─", width))
	}
	return ansi.Hardwrap(splash, max(1, width), true) + "\n" + rule
}
