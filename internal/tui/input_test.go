package tui

import "testing"

func TestIncompleteSequence(t *testing.T) {
	for in, want := range map[string]bool{
		"abc":                       false,
		"\x1b":                      true,
		"\x1b[":                     true,
		"\x1b[<35;12":               true,
		"\x1b[<35;123;58M":          false,
		"\x1b[<35;123;58Mx\x1b[<35": true,
		"\x1b[A":                    false,
		"\x1bO":                     true,
		"\x1bOP":                    false,
		"\x1ba":                     false,
	} {
		if got := incompleteSequence([]byte(in)); got != want {
			t.Errorf("incompleteSequence(%q) = %v, want %v", in, got, want)
		}
	}
}
