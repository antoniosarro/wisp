package tui

import "bytes"

// incompleteSequence reports whether b ends inside an escape sequence: a
// bare ESC, an SS3 prefix, or a CSI sequence without its final byte.
func incompleteSequence(b []byte) bool {
	i := bytes.LastIndexByte(b, 0x1b)
	if i < 0 {
		return false
	}
	seq := b[i:]
	switch {
	case len(seq) == 1:
		return true
	case seq[1] == 'O':
		return len(seq) == 2
	case seq[1] != '[':
		return false // Alt+key or a terminator
	}
	for _, c := range seq[2:] {
		if c >= 0x40 && c <= 0x7e {
			return false
		}
	}
	return true
}
