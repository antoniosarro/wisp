package tui

import "github.com/antoniosarro/wisp/internal/termsafe"

// sanitize and sanitizeJSON keep model and tool text from driving the
// terminal (package termsafe).
var (
	sanitize     = termsafe.Strip
	sanitizeJSON = termsafe.JSON
)

// sanitized is a copy of b with the text it shows cleaned. Blocks keep
// their text as it arrived and are cleaned when rendered: an escape
// sequence split across stream deltas is only whole, and so only
// removable, once they are joined.
func (b *block) sanitized() *block {
	c := *b
	c.text, c.reasoningText, c.toolResult = sanitize(b.text), sanitize(b.reasoningText), sanitize(b.toolResult)
	c.toolName = sanitize(b.toolName)
	c.toolArgs = sanitizeJSON(b.toolArgs, sanitize)
	return &c
}
