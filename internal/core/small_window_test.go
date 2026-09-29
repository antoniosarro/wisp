package core

import (
	"context"
	"strings"
	"testing"
)

// A small window mostly taken by the system prompt leaves a budget of
// almost nothing: requests must still work, and results must be clipped.
func TestSmallWindowMostlyFixed(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()) // Clip saves the full output there
	p := &replyProvider{replies: []string{"ok"}}
	l := &Loop{Provider: p, ContextWindow: 4096, AutoCompact: true, System: strings.Repeat("word ", 50), History: turns(3)}
	if b := l.historyBudget(); b > 16 {
		t.Fatalf("budget = %d; the test needs a nearly full window", b)
	}
	if _, err := l.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("run: %v", err)
	}
	in := strings.Repeat("line of output\n", 5000)
	if out := l.fitResult(in); len(out) >= len(in)/10 {
		t.Errorf("result kept %d of %d bytes", len(out), len(in))
	}
	if out := (&Loop{}).fitResult(in); out != in {
		t.Error("clipped with the window unknown")
	}
}
