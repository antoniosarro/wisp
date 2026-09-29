// Package textfmt shortens and formats text for the model, spans, and
// tool output without splitting UTF-8 characters.
package textfmt

import (
	"fmt"
	"unicode/utf8"
)

// Prefix returns the longest prefix of s that is at most n bytes long
// and does not split a UTF-8-encoded rune. If len(s) <= n, Prefix
// returns s unchanged. Otherwise the result may be shorter than n bytes,
// and it is empty if no rune boundary falls within the first n bytes.
//
// Prefix works on bytes, not grapheme clusters, so it may separate a base
// character from a following combining mark. If s is not valid UTF-8, any
// byte that is not a continuation byte counts as a rune boundary
// (see [utf8.RuneStart]).
//
// Prefix panics if n is negative.
func Prefix(s string, n int) string {
	if n < 0 {
		panic("Prefix: negative length")
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Cut shortens s to at most n bytes, marking the cut with "…".
// If len(s) <= n, s is returned unchanged. Otherwise, the result
// contains the longest prefix of s that fits within n bytes (without
// splitting a UTF-8 rune) followed by the "…" ellipsis character.
//
// Cut works on bytes, not grapheme clusters, so it may separate a base
// character from a following combining mark. If s is not valid UTF-8, any
// byte that is not a continuation byte counts as a rune boundary
// (see [utf8.RuneStart]).
//
// Cut panics if n is negative (delegated to [Prefix]).
func Cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return Prefix(s, n) + "…"
}

// CutRunes shortens s to at most n runes, replacing the last rune with "…"
// when s has more than n runes. If len([]rune(s)) <= n, s is returned
// unchanged.
func CutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// Size formats a byte count: "512 B", "1.5 KB", "3.0 MB".
func Size(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, suffix := float64(n)/unit, "KB"
	for _, s := range []string{"MB", "GB", "TB"} {
		if value < unit {
			break
		}
		value, suffix = value/unit, s
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}
