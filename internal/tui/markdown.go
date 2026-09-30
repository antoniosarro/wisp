package tui

import (
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourStyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// fenceRe matches a closed, column-0 fenced code block. Indented fences
// belong to a list or quote and are left for glamour to render inline;
// unterminated (still streaming) fences don't match yet.
var fenceRe = regexp.MustCompile(`(?sm)^` + "```" + `([a-zA-Z0-9_+-]*)\r?\n(.*?)^` + "```" + `[ \t]*$`)

// glamourRenderers caches renderers by wrap width. Only bubbletea's
// Update goroutine uses it, so no locking.
var glamourRenderers = map[int]*glamour.TermRenderer{}

// glamourRendererFor returns a cached renderer for width, with the document
// margin removed since chatMarginLeft already indents answers. It returns
// nil if construction fails, meaning render unstyled.
func glamourRendererFor(width int) *glamour.TermRenderer {
	if r, ok := glamourRenderers[width]; ok {
		return r
	}

	cfg := glamourStyles.DarkStyleConfig
	if lightTheme {
		cfg = glamourStyles.LightStyleConfig
	}
	zero := uint(0)
	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	applyPalette(&cfg)

	r, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(width))
	if err != nil {
		r = nil
	}
	if len(glamourRenderers) >= 32 {
		clear(glamourRenderers)
	}
	glamourRenderers[width] = r
	return r
}

// applyPalette recolors glamour's markdown styles with the app palette.
func applyPalette(cfg *glamouransi.StyleConfig) {
	color := func(c lipgloss.Color) *string { s := string(c); return &s }
	cfg.Heading.Color = color(colorLavender)
	cfg.H1.Color = color(colorText)
	cfg.H1.BackgroundColor = color(colorViolet)
	cfg.Code.Color = color(colorCyan)
	cfg.Code.BackgroundColor = color(colorPanel)
	cfg.Link.Color = color(colorCyan)
	cfg.LinkText.Color = color(colorLavender)
	cfg.HorizontalRule.Color = color(colorMuted)
	cfg.BlockQuote.Color = color(colorDim)
	cfg.Item.Color = color(colorText)
	cfg.Enumeration.Color = color(colorMagenta)
}

// renderMarkdownAnswer renders prose with glamour and top-level fenced code
// blocks as highlighted cards.
func renderMarkdownAnswer(text string, inner int) string {
	return renderMarkdown(text, inner, false)
}

// renderMarkdown renders like renderMarkdownAnswer. live marks text still
// streaming: its last part changes every frame, so it isn't cached, where
// it would only fill the cache with half-written answers.
func renderMarkdown(text string, inner int, live bool) string {
	matches := fenceRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		if live {
			return liveProse(text, inner)
		}
		return cachedProse(text, inner)
	}

	var parts []string
	pos := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		langStart, langEnd := m[2], m[3]
		codeStart, codeEnd := m[4], m[5]

		if prose := text[pos:start]; strings.TrimSpace(prose) != "" {
			parts = append(parts, cachedProse(prose, inner))
		}

		lang := text[langStart:langEnd]
		code := strings.Trim(text[codeStart:codeEnd], "\n")
		parts = append(parts, cachedPart(partKey{code, lang, true, inner}, func() string { return renderCodeBlock(code, lang, inner) }))

		pos = end
	}
	if rest := text[pos:]; strings.TrimSpace(rest) != "" {
		if live {
			parts = append(parts, liveProse(rest, inner))
		} else {
			parts = append(parts, cachedProse(rest, inner))
		}
	}

	return strings.Join(parts, "\n")
}

// partCache memoizes rendered markdown parts: while an answer streams,
// every part but the last is unchanged from one frame to the next. Like
// glamourRenderers, only bubbletea's Update goroutine uses it.
var partCache = map[partKey]string{}

// partKey identifies a rendered part: its text, and how it was rendered.
type partKey struct {
	text, lang string
	code       bool
	width      int
}

// cachedPart returns the part k identifies, rendering it on a miss.
func cachedPart(k partKey, render func() string) string {
	if out, ok := partCache[k]; ok {
		return out
	}
	if len(partCache) >= 512 {
		clear(partCache)
	}
	out := render()
	partCache[k] = out
	return out
}

// cachedProse is renderProse through the part cache.
func cachedProse(text string, inner int) string {
	return cachedPart(partKey{text: text, width: inner}, func() string { return renderProse(text, inner) })
}

// liveProse renders streaming prose. All but its last paragraph is final,
// so that part is cached and only the paragraph being written renders each
// frame; otherwise a long answer re-renders whole on every frame. It never
// cuts inside a code fence still open. The complete answer renders whole.
func liveProse(text string, inner int) string {
	head := text
	if i := strings.Index(head, "```"); i >= 0 {
		head = head[:i]
	}
	cut := strings.LastIndex(head, "\n\n")
	if cut <= 0 {
		return renderProse(text, inner)
	}
	stable := cachedProse(text[:cut], inner)
	if rest := text[cut+2:]; strings.TrimSpace(rest) != "" {
		return stable + "\n" + renderProse(rest, inner)
	}
	return stable
}

// renderProse renders markdown within inner cells. Glamour occasionally
// overflows its wrap width by a cell, so it retries slightly narrower.
func renderProse(text string, inner int) string {
	for w := inner; w > max(1, inner-3); w-- {
		r := glamourRendererFor(w)
		if r == nil {
			break
		}
		out, err := r.Render(text)
		if err != nil {
			break
		}
		if out = strings.TrimRight(out, "\n"); widestLine(out) <= inner {
			return out
		}
	}
	return lipgloss.NewStyle().Width(inner).Render(text)
}

// widestLine is the width in cells of s's widest line.
func widestLine(s string) int {
	w := 0
	for _, l := range strings.Split(s, "\n") {
		w = max(w, ansi.StringWidth(l))
	}
	return w
}

// renderCodeBlock draws a fenced code block as a highlighted card.
func renderCodeBlock(code, lang string, inner int) string {
	header := lang
	if header == "" {
		header = "code"
	}
	return boxed(styleToolCard, inner, styleDim.Render(header)+"\n"+highlightCode(code, lang))
}

// highlightCode ANSI-colors code with chroma, guessing the lexer when lang
// is unknown and returning code as-is on failure.
func highlightCode(code, lang string) string {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("dracula")
	if lightTheme {
		style = styles.Get("github")
	}
	if style == nil {
		style = styles.Fallback
	}

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}

	var buf strings.Builder
	if err := formatters.TTY256.Format(&buf, style, iterator); err != nil {
		return code
	}
	return strings.TrimRight(buf.String(), "\n")
}
