package builtin

import (
	"errors"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// Tags whose text htmlToText drops, tags that break a line, and the
// Markdown marks that keep headings recognizable.
var (
	skippedTags = map[string]bool{"script": true, "style": true, "noscript": true, "svg": true, "template": true, "iframe": true}
	blockTags   = map[string]bool{
		"p": true, "div": true, "br": true, "tr": true, "section": true, "article": true, "header": true,
		"footer": true, "nav": true, "main": true, "aside": true, "pre": true, "blockquote": true,
		"table": true, "ul": true, "ol": true, "dl": true, "dt": true, "dd": true, "hr": true, "title": true,
	}
	headingMarks = map[string]string{"h1": "# ", "h2": "## ", "h3": "### ", "h4": "#### ", "h5": "##### ", "h6": "###### "}
)

// htmlToText extracts readable text, keeping headings and list items as
// Markdown. It is a tokenizer pass, not a renderer: no CSS, no scripts.
func htmlToText(r io.Reader) (string, error) {
	z := html.NewTokenizer(r)
	var b strings.Builder
	skip := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if errors.Is(z.Err(), io.EOF) {
				return tidyText(b.String()), nil
			}
			return "", z.Err()
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			start := tt != html.EndTagToken
			switch {
			case skippedTags[tag]:
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
			case headingMarks[tag] != "":
				b.WriteString("\n")
				if start {
					b.WriteString(headingMarks[tag])
				}
			case tag == "li" && start:
				b.WriteString("\n- ")
			case blockTags[tag]:
				b.WriteString("\n")
			}
		case html.TextToken:
			if skip == 0 {
				b.WriteString(" ")
				b.WriteString(string(z.Text()))
				b.WriteString(" ")
			}
		}
	}
}

// tidyText collapses whitespace within lines and runs of blank lines.
func tidyText(s string) string {
	var out []string
	blank := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
