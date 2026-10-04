package core

import (
	"strings"
	"testing"
)

func TestRepeatWatch(t *testing.T) {
	const loop = "Let me find the go module cache path."
	for _, tc := range []struct {
		name   string
		stream string
		want   string // "" for no loop
		at     int    // occurrence of want that trips it
	}{
		{name: "a sentence said again and again", stream: strings.Repeat(loop+"\n\n", 30), want: loop, at: repeatLimit},
		{name: "a sentence ending mid-line", stream: strings.Repeat(loop+" Let me check. ", 30), want: loop, at: repeatLimit},
		{name: "spacing differs", stream: strings.Repeat("Let me find  the go\tmodule cache path.\n", 4) + strings.Repeat(loop+"\n", 4), want: loop, at: repeatLimit},
		{name: "a line of code quoted again", stream: strings.Repeat("return filepath.Join(real, rest)\nfor dir := path; ; dir = filepath.Dir(dir) {\n", 30)},
		{name: "a short filler", stream: strings.Repeat("Let me check.\n", 30)},
		{name: "two sentences, each under the limit", stream: strings.Repeat(loop+"\nOK, I'll run the grep and read the test file now.\n", repeatLimit-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Fed a few bytes at a time, as a stream arrives.
			var w repeatWatch
			got, fed := "", 0
			for i := 0; i < len(tc.stream) && got == ""; i += 3 {
				fed = min(i+3, len(tc.stream))
				got = w.add(tc.stream[i:fed])
			}
			if got != tc.want {
				t.Fatalf("add = %q, want %q", got, tc.want)
			}
			if tc.want != "" {
				if n := strings.Count(strings.Join(strings.Fields(tc.stream[:fed]), " "), tc.want); n != tc.at {
					t.Errorf("tripped after %d occurrences, want %d", n, tc.at)
				}
			}
		})
	}
}

func TestRepeatReminderClipsLongSentences(t *testing.T) {
	r := repeatReminder(strings.Repeat("é", 100)) // 200 bytes
	if !strings.Contains(r, "…") || !strings.Contains(r, "8 times") {
		t.Errorf("reminder = %q", r)
	}
}
