package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

// halfFull is finished turns filling about 60% of a 16K window's budget:
// past the presummarize threshold, short of the mask trigger.
func halfFull(t *testing.T) *Loop {
	t.Helper()
	l := &Loop{ContextWindow: 16384, AutoCompact: true, Presummarize: true, History: turns(12)}
	budget := l.historyBudget()
	if p := l.projectedHistory(); p <= budget*presummarizePct/100 || p > budget*maskTriggerPct/100 {
		t.Fatalf("history %d not between %d%% and %d%% of budget %d", p, presummarizePct, maskTriggerPct, budget)
	}
	return l
}

func TestPresummaryIsUsedByTheNextCompaction(t *testing.T) {
	l := halfFull(t)
	p := &replyProvider{replies: []string{"done", "## Goal\n- prepared"}}
	l.Provider = p
	if _, err := l.Run(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	if l.pre == nil {
		t.Fatal("no background summary after the turn")
	}
	<-l.pre.done
	summaryReq := p.reqs[1]
	if summaryReq.MaxTokens != l.summaryCap() || !strings.Contains(summaryReq.Messages[len(summaryReq.Messages)-1].Content, "## Critical context") {
		t.Errorf("background request = %+v", summaryReq)
	}

	if err := l.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(p.reqs) != 2 {
		t.Errorf("%d requests: compaction didn't use the prepared summary", len(p.reqs))
	}
	c := l.Compacted
	if !c.Precomputed || !strings.Contains(c.Text(), "- prepared") || l.pre != nil {
		t.Errorf("precomputed %v, summary %q", c.Precomputed, c.Text())
	}
}

func TestPresummaryNotUsedWithFocusOrAfterAnotherCompaction(t *testing.T) {
	l := halfFull(t)
	p := &replyProvider{replies: []string{"done", "## Goal\n- prepared", "## Goal\n- focused"}}
	l.Provider = p
	if _, err := l.Run(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	pre := l.pre
	<-pre.done
	if err := l.Compact(context.Background(), "the API"); err != nil {
		t.Fatal(err)
	}
	if l.Compacted.Precomputed || !strings.Contains(l.Compacted.Text(), "- focused") {
		t.Errorf("focused compaction used the prepared summary: %q", l.Compacted.Text())
	}
	if l.usable(pre) {
		t.Error("a summary made before the last compaction is still usable")
	}
}

func TestYieldPresummary(t *testing.T) {
	running := func() (*presummary, *bool) {
		cancelled := false
		pre := &presummary{done: make(chan struct{})}
		pre.cancel = func() {
			if !cancelled {
				cancelled = true
				close(pre.done)
			}
		}
		return pre, &cancelled
	}

	// Not needed by the coming turn: cancelled, freeing the server.
	l := halfFull(t)
	pre, cancelled := running()
	pre.cut = l.cutPoint(l.tailTokens(), 0)
	l.pre = pre
	l.yieldPresummary("short")
	if !*cancelled || l.pre != nil {
		t.Errorf("unneeded: cancelled %v, kept %v", *cancelled, l.pre != nil)
	}

	// Needed at once: left to finish.
	pre, cancelled = running()
	pre.cut = l.cutPoint(l.tailTokens(), 0)
	l.pre = pre
	l.yieldPresummary(strings.Repeat("a long request ", 1500))
	if *cancelled || l.pre != pre {
		t.Errorf("needed: cancelled %v, kept %v", *cancelled, l.pre == pre)
	}

	// Finished: kept for a later turn.
	pre.cancel()
	*cancelled = false
	l.yieldPresummary("short")
	if l.pre != pre {
		t.Error("a finished summary was dropped")
	}
}

func TestPresummaryOnlyWhenWorthIt(t *testing.T) {
	for name, l := range map[string]*Loop{
		"not local":  {ContextWindow: 16384, AutoCompact: true, History: turns(12)},
		"under half": {ContextWindow: 16384, AutoCompact: true, Presummarize: true, History: turns(4)},
		"no window":  {AutoCompact: true, Presummarize: true, History: turns(12)},
		"no auto":    {ContextWindow: 16384, Presummarize: true, History: turns(12)},
	} {
		l.Provider = &replyProvider{replies: []string{"x"}}
		l.presummarize()
		if l.pre != nil {
			t.Errorf("%s: started a background summary", name)
			l.StopPresummary()
		}
	}
}

func TestRehydrate(t *testing.T) {
	t.Chdir(t.TempDir())
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(".", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\n\nfunc A() {}\n")
	write("big.go", strings.Repeat("some line of source code\n", 2000))
	write("c.go", "package c\n")
	write("bin", "\x00\x01")
	var lg Ledger
	for i, path := range []string{"a.go", "big.go", "c.go", "bin", "gone.go"} {
		id := fmt.Sprint("w", i)
		lg.Update([]model.Message{calls(call(id, "write", fmt.Sprintf(`{"path":%q}`, path))), result(id, "ok", false)})
	}

	l := &Loop{ContextWindow: 65536}
	files := l.rehydrate(lg)
	if len(files) != 2 || files[0].Path != "c.go" || files[1].Path != "a.go" {
		t.Fatalf("files = %+v, want c.go then a.go", files)
	}
	want := "     1\tpackage a\n     2\t\n     3\tfunc A() {}"
	if files[1].Content != want {
		t.Errorf("a.go = %q, want %q", files[1].Content, want)
	}
	c := Compaction{Summary: "S", Files: files}
	if text := c.render(100); !strings.HasSuffix(text, "<file path=\"a.go\">\n"+want+"\n</file>") {
		t.Errorf("rendered = %q", text)
	}
	if files := (&Loop{ContextWindow: 16384}).rehydrate(lg); files != nil {
		t.Errorf("small window rehydrated %v", files)
	}
}
