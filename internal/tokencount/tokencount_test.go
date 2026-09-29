package tokencount

import (
	"strings"
	"testing"
)

func TestCount(t *testing.T) {
	// cl100k is deterministic, so exact counts pin the real tokenizer;
	// the len/4 fallback would give 25 for the 100-byte case.
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world foo bar", 4},
		{strings.Repeat("a", 100), 13},
		{"héllo 世界 🎉", 10},
	}
	for _, tt := range tests {
		if got := Count(tt.in); got != tt.want {
			t.Errorf("Count(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
