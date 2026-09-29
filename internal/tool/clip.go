package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/antoniosarro/wisp/internal/textfmt"
)

// MaxOutputBytes is the output budget of one tool call. Longer output
// keeps its head and its tail, where errors usually are, and is saved in
// full to a file the model can read or grep.
const MaxOutputBytes = 30_000

// spillPattern names the files Clip saves full output to.
const spillPattern = "wisp-output-*.txt"

// spillMaxAge is how long saved output is kept: long enough for the
// session that produced it, then pruned so the files don't pile up.
const spillMaxAge = 24 * time.Hour

// Clip fits out into limit bytes. Longer output keeps the first third and
// the last two thirds of the budget (a command's errors and summary are
// usually at the end), cut at line breaks, or at least between characters,
// and is saved whole to a temporary file the note names. captured > 0 says
// the capture itself stopped at that many bytes per stream, so even the
// saved file is not all of it.
func Clip(out string, limit, captured int) string {
	if len(out) <= limit {
		return out
	}
	pruneSpills()
	saved := "the full output could not be saved"
	if f, err := os.CreateTemp("", spillPattern); err == nil {
		_, werr := f.WriteString(out)
		if cerr := f.Close(); werr == nil && cerr == nil {
			saved = "full output saved to " + f.Name() + "; read or grep it for the rest"
			if captured > 0 {
				saved = fmt.Sprintf("output saved to %s, itself cut at %d MB per stream; read or grep it for more", f.Name(), captured>>20)
			}
		}
	}
	head := textfmt.Prefix(out, limit/3)
	if i := strings.LastIndexByte(head, '\n'); i > 0 {
		head = head[:i]
	}
	start := len(out) - (limit - limit/3)
	for start < len(out) && !utf8.RuneStart(out[start]) {
		start++
	}
	tail := out[start:]
	if i := strings.IndexByte(tail, '\n'); i >= 0 && i < len(tail)-1 {
		tail = tail[i+1:]
	}
	return fmt.Sprintf("%s\n... (%d bytes omitted; %s)\n%s", head, len(out)-len(head)-len(tail), saved, tail)
}

// pruneSpills removes saved output older than spillMaxAge. It runs when
// output is saved, so only sessions that produce long output pay for it.
func pruneSpills() {
	paths, _ := filepath.Glob(filepath.Join(os.TempDir(), spillPattern))
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) > spillMaxAge {
			_ = os.Remove(p)
		}
	}
}
