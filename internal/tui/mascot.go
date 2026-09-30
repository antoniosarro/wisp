package tui

import (
	"bytes"
	"cmp"
	"image"
	"image/png"
	"math/rand/v2"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/assets"
)

// The mascot is a small animated sprite in the bottom-right corner, over
// everything else.
// Its frame and caption follow what the agent is doing, so a glance at the
// panel says more than the token counters do.

// mascotState is what the agent is doing, as far as the mascot is concerned.
type mascotState int

const (
	mascotIdle     mascotState = iota // nothing running
	mascotThinking                    // reasoning tokens are streaming
	mascotWriting                     // answer text is streaming
	mascotTool                        // a tool call is in flight
	mascotAgent                       // a sub-agent is running
	mascotWaiting                     // a risky call waits for approval
	mascotSleeping                    // idle for a while

	// Easter eggs, played when a message mentions them (mascotEggs).
	mascotWaving
	mascotLoved
	mascotSpooky
	mascotPartying
	mascotDancing
	mascotSipping
)

// Ticks are the spinner's, 100 ms each; a strip frame lasts two.
const (
	// mascotSettleTicks is how long a state must be wanted before it shows,
	// so work that ends within it, like a fast tool call, never flashes.
	mascotSettleTicks = 3
	// mascotSleepTicks without a turn or a key press puts the mascot to
	// sleep: a minute.
	mascotSleepTicks = 600
	// mascotEggTicks is how long an easter egg plays: two loops of a
	// 6-frame strip.
	mascotEggTicks = 24
	// Once nothing has happened for mascotEggQuietTicks (10 s: no turn, key
	// press or egg), each tick plays a random egg with odds 1 in
	// mascotEggOdds, about one every 30 s.
	mascotEggQuietTicks = 100
	mascotEggOdds       = 300
)

// mascotRoll returns a random int in [0, n); tests replace it.
var mascotRoll = rand.IntN

// mascotEggs are the easter eggs and the words in a message that play them.
// Words match whole, ignoring case and surrounding punctuation; phrases
// match as consecutive words.
var mascotEggs = []struct {
	state mascotState
	words []string
}{
	{mascotWaving, []string{"hello", "hi", "hey", "hiya", "bye", "goodbye"}},
	{mascotLoved, []string{"thanks", "thank you", "thx", "ty", "love you", "<3", "❤️"}},
	{mascotSpooky, []string{"boo", "spooky", "ghost", "👻"}},
	{mascotPartying, []string{"party", "yay", "hooray", "celebrate", "🎉"}},
	{mascotDancing, []string{"dance", "music", "🎵", "🎶"}},
	{mascotSipping, []string{"coffee", "tea", "break", "☕"}},
	{mascotSleeping, []string{"good night", "goodnight", "gn", "sleep"}},
}

// mascotFrames holds each state's animation; the first frame is the rest
// pose, which idle and the static states show. Frames are single cells wide
// so the sprite never shifts horizontally while it animates.
var mascotFrames = map[mascotState][]string{
	mascotIdle:     {"-", "o", "-", "o"},
	mascotThinking: {".", "o", "O", "o"},
	mascotWriting:  {"*", "*", "+", "*"},
	mascotTool:     {"+", "x", "+", "x"},
	mascotAgent:    {">", "»", ">", "»"},
	mascotWaiting:  {"?", "?", "!", "?"},
	mascotSleeping: {"z", "z", "Z", "z"},
	mascotWaving:   {"o", "/", "o", "\\"},
	mascotLoved:    {"♥", "♡", "♥", "♡"},
	mascotSpooky:   {"O", "!", "O", "!"},
	mascotPartying: {"*", "+", "*", "x"},
	mascotDancing:  {"♪", "♫", "♪", "♫"},
	mascotSipping:  {"c", "~", "c", "~"},
}

// mascotCaptions names each state's sprite strip (assets/mascot/<caption>.png)
// and is the status shown next to the text sprite, unless mascotPhrases
// words it better.
var mascotCaptions = map[mascotState]string{
	mascotIdle:     "idle",
	mascotThinking: "thinking",
	mascotWriting:  "writing",
	mascotTool:     "tool",
	mascotAgent:    "agent",
	mascotWaiting:  "waiting",
	mascotSleeping: "sleeping",
	mascotWaving:   "waving",
	mascotLoved:    "loved",
	mascotSpooky:   "spooky",
	mascotPartying: "partying",
	mascotDancing:  "dancing",
	mascotSipping:  "sipping",
}

// mascotPhrases words the states whose caption doesn't read as "wisp is …".
var mascotPhrases = map[mascotState]string{
	mascotTool:  "using a tool",
	mascotAgent: "delegating",
}

// mascotColors tints the sprite per state, so the mood reads before the text.
func mascotColor(s mascotState) lipgloss.Color {
	switch s {
	case mascotThinking:
		return colorLavender
	case mascotWriting:
		return colorCyan
	case mascotTool:
		return colorAmber
	case mascotAgent:
		return colorMagenta
	case mascotWaiting, mascotSipping:
		return colorAmber
	case mascotLoved:
		return colorRose
	case mascotSpooky, mascotWaving:
		return colorLavender
	case mascotPartying:
		return colorMagenta
	case mascotDancing:
		return colorCyan
	}
	return colorDim
}

// mascotFrame is the sprite for a state at animation step n.
func mascotFrame(s mascotState, n int) string {
	frames := mascotFrames[s]
	return frames[((n%len(frames))+len(frames))%len(frames)]
}

// On kitty-graphics terminals the mascot is drawn from assets/mascot
// instead, each frame uploaded as its own image, so animating is only a
// change of the placeholder cells' color.
const (
	mascotImageBase = 100 // ids up to 255: they are carried as 256-color foregrounds
	mascotCols      = 12  // cells are about twice as tall as wide, so 12×6 is square
	mascotRows      = 6
)

// mascotImages is each state's frame image ids, once UploadImages has sent
// them; nil draws the text sprite.
var mascotImages map[mascotState][]int

// uploadMascot writes the uploads of every state's frames, cut from
// assets/mascot/<caption>.png and recolored in colors, and returns their
// ids. It returns nil if any strip can't be read, so the mascot is never
// drawn half from images.
func uploadMascot(b *strings.Builder, colors termColors) map[mascotState][]int {
	images := map[mascotState][]int{}
	id := mascotImageBase
	for s, name := range mascotCaptions {
		data, err := assets.Mascot.ReadFile("mascot/" + name + ".png")
		if err != nil {
			return nil
		}
		strip, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil
		}
		if p, ok := strip.(*image.Paletted); ok {
			p.Palette = colors.mascotPalette(p.Palette)
		}
		sub, ok := strip.(interface {
			SubImage(image.Rectangle) image.Image
		})
		if !ok {
			return nil
		}
		r := strip.Bounds()
		for x := r.Min.X; x+r.Dy() <= r.Max.X; x += r.Dy() {
			var frame bytes.Buffer
			if err := png.Encode(&frame, sub.SubImage(image.Rect(x, r.Min.Y, x+r.Dy(), r.Max.Y))); err != nil {
				return nil
			}
			writeKittyImage(b, id, mascotCols, mascotRows, frame.Bytes())
			images[s] = append(images[s], id)
			id++
		}
	}
	return images
}

// renderMascot draws the sprite. The image sprite advances a frame every
// other tick and speaks for itself; the text one is captioned and gets a
// fixed-width trail while the agent works, so it never shifts as it animates.
func renderMascot(s mascotState, n int) string {
	if frames := mascotImages[s]; len(frames) > 0 {
		return renderPlaceholders(frames[(n/2)%len(frames)], mascotCols, mascotRows)
	}
	style := lipgloss.NewStyle().Foreground(mascotColor(s)).Bold(true)
	sprite := ""
	if busy(s) {
		trail := "· "
		if n%2 == 1 {
			trail = "··"
		}
		sprite = style.Render(trail)
	}
	sprite += style.Render(mascotFrame(s, n))
	return sprite + "  " + styleDim.Render("wisp is "+cmp.Or(mascotPhrases[s], mascotCaptions[s]))
}

// overlayMascot paints the mascot over the bottom-right corner of main,
// inside its border, on top of whatever is there, so it shows in every view.
// A main too small to hold it inside the border is returned unchanged.
func (m *Model) overlayMascot(main string) string {
	rows := strings.Split(renderMascot(m.mascot, m.mascotFrameTick()), "\n")
	lines := strings.Split(main, "\n")
	w := 0
	for _, r := range rows {
		w = max(w, ansi.StringWidth(r))
	}
	top, left := len(lines)-1-len(rows), ansi.StringWidth(lines[0])-1-w
	if top < 1 || left < 1 {
		return main
	}
	return placeOver(main, strings.Join(rows, "\n"), top, left)
}

// busy reports whether the state is one the mascot animates for.
func busy(s mascotState) bool {
	switch s {
	case mascotThinking, mascotWriting, mascotTool, mascotAgent, mascotWaiting:
		return true
	}
	return false
}

// mascotState is the state the model's activity calls for, most urgent
// first: a pending approval, then a playing easter egg, then work; with
// nothing going on the mascot idles, and sleeps once that has lasted.
func (m *Model) mascotState() mascotState {
	switch {
	case len(m.pending) > 0:
		return mascotWaiting
	case m.mascotTick < m.eggUntil:
		return m.egg
	case m.inTurn:
		switch {
		case m.subAgentActive():
			return mascotAgent
		case m.toolRunning():
			return mascotTool
		case m.streamingReasoning():
			return mascotThinking
		case m.streamingAnswer():
			return mascotWriting
		}
		return mascotThinking // a request is in flight but nothing streamed yet
	case m.mascotTick-max(m.turnEndTick, m.keyTick) >= mascotSleepTicks:
		return mascotSleeping
	}
	return mascotIdle
}

// stepMascot advances the shown state on a tick. A new state shows once it
// has been wanted for mascotSettleTicks ticks in a row, and its animation
// starts from the first frame.
func (m *Model) stepMascot() {
	m.maybeRandomEgg()
	want := m.mascotState()
	if want != m.mascotWant {
		m.mascotWant, m.mascotWantTicks = want, 0
	}
	m.mascotWantTicks++
	if want != m.mascot && m.mascotWantTicks >= mascotSettleTicks {
		m.mascot, m.mascotSince = want, m.mascotTick
	}
}

// mascotFrameTick is the shown state's animation step.
func (m *Model) mascotFrameTick() int { return m.mascotTick - m.mascotSince }

// maybeRandomEgg sometimes plays a random easter egg while wisp is quiet,
// sleeping included; going to sleep has its own trigger, so it isn't one.
func (m *Model) maybeRandomEgg() {
	quiet := m.mascotTick - max(m.turnEndTick, m.keyTick, m.eggUntil)
	if m.inTurn || len(m.pending) > 0 || quiet < mascotEggQuietTicks || mascotRoll(mascotEggOdds) != 0 {
		return
	}
	var eggs []mascotState
	for _, e := range mascotEggs {
		if e.state != mascotSleeping {
			eggs = append(eggs, e.state)
		}
	}
	m.egg, m.eggUntil = eggs[mascotRoll(len(eggs))], m.mascotTick+mascotEggTicks
}

// playEgg starts the easter egg a message mentions, if any.
func (m *Model) playEgg(message string) {
	var words []string
	for _, w := range strings.Fields(strings.ToLower(message)) {
		if w = strings.Trim(w, ".,!?;:'\"()"); w != "" {
			words = append(words, w)
		}
	}
	text := " " + strings.Join(words, " ") + " "
	for _, egg := range mascotEggs {
		for _, w := range egg.words {
			if strings.Contains(text, " "+w+" ") {
				m.egg, m.eggUntil = egg.state, m.mascotTick+mascotEggTicks
				return
			}
		}
	}
}

// subAgentActive reports whether a sub-agent is running or queued.
func (m *Model) subAgentActive() bool {
	for _, r := range m.agentRuns {
		if runActive(r) {
			return true
		}
	}
	return false
}

// toolRunning reports whether a tool call in the shown conversation is in
// flight, which covers the wait while its approval is being decided.
func (m *Model) toolRunning() bool {
	for _, b := range *m.shown() {
		if b.kind == blockToolCall && b.toolStatus == toolRunning {
			return true
		}
	}
	return false
}

// streamingReasoning reports whether reasoning tokens are arriving.
func (m *Model) streamingReasoning() bool {
	b := m.blocks.last(blockReasoning)
	return b != nil && !b.reasoningDone
}

// streamingAnswer reports whether answer text is arriving.
func (m *Model) streamingAnswer() bool {
	b := m.blocks.last(blockAnswer)
	return b != nil && b.stream != nil
}
