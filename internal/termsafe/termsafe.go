// Package termsafe keeps text from the model, tools, and endpoints from
// driving the terminal it is printed to: raw escape sequences in it could
// write the clipboard (OSC 52), retitle the window, switch modes, or
// redraw lines, e.g. to make an approval show a command other than the
// one it runs.
package termsafe

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Strip removes ANSI escape sequences, control characters, and invalid
// UTF-8 bytes from s, normalizing \r\n to \n. It returns s unchanged if
// it contains none of them.
func Strip(s string) string {
	if !hasControl(s) {
		return s
	}
	s = strings.ReplaceAll(ansi.Strip(s), "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, s)
}

// Show replaces control characters in s with Unicode symbol representations
// (e.g. 0x00 → 0x2400, 0x7f → ␡) so they are visible but harmless when
// printed to the terminal. It returns s unchanged if it contains no
// control characters.
func Show(s string) string {
	if !hasControl(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case !isControl(r):
			return r
		case r < 0x20:
			return 0x2400 + r
		case r == 0x7f:
			return '␡'
		}
		return '�'
	}, s)
}

// JSON sanitizes raw JSON by applying clean to every string value and
// key. If the JSON is empty or contains no control characters or \u00xx
// escape sequences, it is returned unchanged. If unmarshalling fails,
// the raw bytes are cleaned as a single string instead.
func JSON(raw json.RawMessage, clean func(string) string) json.RawMessage {
	if len(raw) == 0 || !strings.Contains(string(raw), `\u00`) && !strings.Contains(string(raw), `\r`) && !hasControl(string(raw)) {
		return raw
	}
	// UseNumber keeps large integers exact instead of rounding through float64.
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil || !atEOF(dec) {
		return json.RawMessage(clean(string(raw)))
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(cleanValue(v, clean)) != nil {
		return raw
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n"))
}

// atEOF reports whether dec has nothing left but whitespace, matching
// json.Unmarshal's rejection of trailing data.
func atEOF(dec *json.Decoder) bool {
	_, err := dec.Token()
	return err == io.EOF
}

// cleanValue recursively applies clean to every string value and key in v.
func cleanValue(v any, clean func(string) string) any {
	switch v := v.(type) {
	case string:
		return clean(v)
	case []any:
		for i := range v {
			v[i] = cleanValue(v[i], clean)
		}
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[clean(k)] = cleanValue(x, clean)
		}
		return out
	}
	return v
}

// hasControl reports whether s contains any control characters or invalid
// UTF-8. Invalid bytes must go through strings.Map, which replaces them with
// U+FFFD: a lone 0x9b is an 8-bit CSI to terminals that honour C1 bytes.
func hasControl(s string) bool {
	if !utf8.ValidString(s) {
		return true
	}
	for _, r := range s {
		if isControl(r) {
			return true
		}
	}
	return false
}

// isControl reports whether r is a control character (C0/C1 range, excluding
// newline and horizontal tab, which are allowed).
func isControl(r rune) bool {
	return r < 0x20 && r != '\n' && r != '\t' || r >= 0x7f && r <= 0x9f
}
