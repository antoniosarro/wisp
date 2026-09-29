package core

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tokencount"
)

// replyProvider answers requests with replies in order, the last one
// repeated; a nil error in errs means answer.
type replyProvider struct {
	replies []string
	errs    []error
	reqs    []model.Request
}

func (p *replyProvider) Stream(_ context.Context, req model.Request) (<-chan model.Event, error) {
	n := len(p.reqs)
	p.reqs = append(p.reqs, req)
	if n < len(p.errs) && p.errs[n] != nil {
		return nil, p.errs[n]
	}
	ch := make(chan model.Event, 2)
	ch <- model.Event{Kind: model.EventTextDelta, Text: p.replies[min(n, len(p.replies)-1)]}
	ch <- model.Event{Kind: model.EventDone}
	close(ch)
	return ch, nil
}

type compactStore struct {
	elideStore
	saved []Compaction
}

func (s *compactStore) SaveCompaction(_ string, c Compaction) error {
	s.saved = append(s.saved, c)
	return nil
}

func (s *compactStore) Compactions(string) ([]Compaction, error) { return s.saved, nil }

// turns is n finished turns, each a user message, a read of a big file,
// and an answer.
func turns(n int) []model.Message {
	var h []model.Message
	for i := range n {
		id := fmt.Sprint("r", i)
		h = append(h,
			user(fmt.Sprintf("turn %d", i)),
			calls(call(id, "read", fmt.Sprintf(`{"path":"f%d.go"}`, i))),
			result(id, bigOutput, false),
			model.Message{Role: model.RoleAssistant, Content: fmt.Sprintf("answer %d", i)},
		)
	}
	return h
}

func TestCutPoint(t *testing.T) {
	l := &Loop{History: turns(6)}
	per := l.historyTokens() / 6
	if got := l.cutPoint(per*10, 0); got != 0 {
		t.Errorf("history within the tail: cut = %d, want 0", got)
	}
	// The last two turns fill the tail: the cut is at the user message
	// starting the earlier of them.
	if got := l.cutPoint(per*2-10, 0); got != 16 {
		t.Errorf("cut = %d, want 16 (the user message of turn 4)", got)
	}
	if got := l.cutPoint(per*2-10, per*5); got != 0 {
		t.Errorf("cut = %d, want 0: less than the least worth summarizing before it", got)
	}

	// One long turn: the cut splits it at an assistant message.
	long := []model.Message{user("do it all")}
	for i := range 12 {
		id := fmt.Sprint("c", i)
		long = append(long, calls(call(id, "read", fmt.Sprintf(`{"path":"g%d.go"}`, i))), result(id, bigOutput, false))
	}
	l = &Loop{History: append(turns(1), long...)}
	cut := l.cutPoint(per, 0)
	if cut <= 4 || l.History[cut].Role != model.RoleAssistant {
		t.Errorf("long turn: cut = %d (%s), want an assistant message inside it", cut, l.History[cut].Role)
	}
}

func TestCompact(t *testing.T) {
	store := &compactStore{elideStore: elideStore{elided: map[int]string{}}}
	p := &replyProvider{replies: []string{"## Goal\n- read files"}}
	var events []CompactEvent
	l := &Loop{Provider: p, Store: store, System: "sys", ContextWindow: 16384, History: turns(12), OnCompact: func(e CompactEvent) { events = append(events, e) }}
	before := l.Messages()

	if err := l.Compact(context.Background(), "keep the file names"); err != nil {
		t.Fatal(err)
	}
	req := p.reqs[0]
	if !reflect.DeepEqual(req.Messages[:len(before)], before) {
		t.Error("summary request doesn't start with the previous request's messages")
	}
	instruction := req.Messages[len(req.Messages)-1].Content
	if !strings.Contains(instruction, "## Critical context") || !strings.Contains(instruction, "keep the file names") || !strings.Contains(instruction, `user's message that begins "turn`) {
		t.Errorf("instruction = %q", instruction)
	}
	if req.MaxTokens != l.summaryCap() {
		t.Errorf("max tokens = %d", req.MaxTokens)
	}

	c := l.Compacted
	if c == nil || len(store.saved) != 1 || len(events) != 2 || events[0].Done || !events[1].Done || events[1].Err != nil {
		t.Fatalf("compacted %v, saved %d, events %+v", c != nil, len(store.saved), events)
	}
	msgs := l.Messages()
	if msgs[0].Content != "sys" || msgs[1].Role != model.RoleUser || !strings.HasPrefix(msgs[1].Content, summaryHeader+"\n\n## Goal\n- read files\n\n"+recordsNote+"\n\n<files-read>\nf0.go") {
		t.Errorf("summary message = %q", msgs[1].Content)
	}
	if !strings.HasSuffix(msgs[1].Content, "\n\n"+l.History[c.FirstKept].Content) {
		t.Error("summary not merged into the first kept user message")
	}
	if len(msgs) != 2+len(l.History)-c.FirstKept-1 {
		t.Errorf("%d messages sent for a tail of %d", len(msgs), len(l.History)-c.FirstKept)
	}
	if c.TokensAfter >= c.TokensBefore {
		t.Errorf("tokens %d → %d", c.TokensBefore, c.TokensAfter)
	}

	// A second compaction updates the first summary and carries its ledger.
	l.History = append(l.History, turns(12)...)
	if err := l.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if instruction := p.reqs[1].Messages[len(p.reqs[1].Messages)-1].Content; !strings.Contains(instruction, "Return that summary updated") {
		t.Errorf("second instruction = %q", instruction)
	}
	if text := l.Compacted.Text(); !strings.Contains(text, "f1.go") || strings.Count(text, ". turn 0\n") != 2 {
		t.Errorf("second summary lost or repeated the first ledger:\n%s", text)
	}
}

func TestCompactFallsBackToLedger(t *testing.T) {
	var done CompactEvent
	p := &replyProvider{errs: []error{errors.New("503"), errors.New("503")}} // the request and its retry with reasoning allowed
	l := &Loop{Provider: p, ContextWindow: 16384, History: turns(12), OnCompact: func(e CompactEvent) { done = e }}
	if err := l.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if done.Err == nil || !strings.Contains(l.Compacted.Text(), summaryFailed) || !strings.Contains(l.Compacted.Text(), "<files-read>") {
		t.Errorf("err %v, summary %q", done.Err, l.Compacted.Text())
	}
}

func TestCompactCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l := &Loop{Provider: &replyProvider{replies: []string{"x"}}, ContextWindow: 16384, History: turns(12)}
	if err := l.Compact(ctx, ""); !errors.Is(err, context.Canceled) || l.Compacted != nil {
		t.Errorf("err %v, compacted %v", err, l.Compacted != nil)
	}
}

func TestSummaryBeforeAssistantIsItsOwnMessage(t *testing.T) {
	l := &Loop{History: turns(2), Compacted: &Compaction{FirstKept: 5, text: "S"}}
	msgs := l.Messages()
	if len(msgs) != 4 || msgs[0].Content != "S" || msgs[1].Role != model.RoleAssistant {
		t.Errorf("messages = %+v", msgs)
	}
}

func TestAutoCompactBeforeStep(t *testing.T) {
	// Big user messages: nothing masking can shrink.
	var h []model.Message
	for i := range 22 {
		h = append(h, user(fmt.Sprint(i, " ", bigOutput)), model.Message{Role: model.RoleAssistant, Content: "ok"})
	}
	p := &replyProvider{replies: []string{"## Goal\n- x", "done"}}
	l := &Loop{Provider: p, ContextWindow: 16384, AutoCompact: true, History: h}
	if l.projectedHistory() <= l.historyBudget()*summarizePct/100 {
		t.Fatalf("history %d not past the summarize trigger of budget %d", l.projectedHistory(), l.historyBudget())
	}
	answer, err := l.Run(context.Background(), "next")
	if err != nil || answer != "done" {
		t.Fatalf("answer %q, err %v", answer, err)
	}
	if l.Compacted == nil || len(p.reqs) != 2 || !strings.HasPrefix(p.reqs[1].Messages[0].Content, summaryHeader) {
		t.Errorf("compacted %v, %d requests", l.Compacted != nil, len(p.reqs))
	}
	if l.projectedHistory() > l.historyBudget()*summarizePct/100 {
		t.Errorf("history %d still past the trigger after compacting", l.projectedHistory())
	}
}

func TestLoadCompaction(t *testing.T) {
	store := &compactStore{saved: []Compaction{{FirstKept: 2, Summary: "## Goal\n- old"}, {FirstKept: 4, Summary: "## Goal\n- x"}}}
	l := &Loop{Store: store, History: turns(3)}
	if err := l.LoadCompaction(); err != nil || l.Compacted == nil || l.Compacted.FirstKept != 4 || len(l.Compactions) != 2 {
		t.Fatalf("err %v, compacted %+v, %d compactions", err, l.Compacted, len(l.Compactions))
	}
	if l.Compactions[0].Text() == "" {
		t.Error("earlier compaction not rendered")
	}
	if !strings.HasPrefix(l.Messages()[0].Content, summaryHeader+"\n\n## Goal") {
		t.Errorf("first message = %q", l.Messages()[0].Content)
	}
	store.saved[1].FirstKept = 99 // past the history: ignored
	l = &Loop{Store: store, History: turns(3)}
	if err := l.LoadCompaction(); err != nil || l.Compacted == nil || l.Compacted.FirstKept != 2 {
		t.Errorf("stale compaction loaded: err %v, compacted %+v", err, l.Compacted)
	}
}

// strictProvider rejects requests asking for no reasoning, as a backend
// that doesn't know the field might.
type strictProvider struct{ replyProvider }

func (p *strictProvider) Stream(ctx context.Context, req model.Request) (<-chan model.Event, error) {
	if req.NoReasoning {
		p.reqs = append(p.reqs, req)
		return nil, errors.New("400: unknown field chat_template_kwargs")
	}
	return p.replyProvider.Stream(ctx, req)
}

func TestSummaryAsksForNoReasoningAndFallsBack(t *testing.T) {
	p := &strictProvider{replyProvider{replies: []string{"## Goal\n- x"}}}
	l := &Loop{Provider: p, ContextWindow: 16384, History: turns(12)}
	if err := l.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(p.reqs) != 2 || !p.reqs[0].NoReasoning || p.reqs[1].NoReasoning {
		t.Fatalf("%d requests, want one without reasoning and one retry with it", len(p.reqs))
	}
	if p.reqs[1].MaxTokens < 8192 || p.reqs[1].MaxTokens <= p.reqs[0].MaxTokens {
		t.Errorf("retry max tokens %d, want room for reasoning over %d", p.reqs[1].MaxTokens, p.reqs[0].MaxTokens)
	}
	if got, want := p.reqs[1].ReasoningTokens, p.reqs[1].MaxTokens-p.reqs[0].MaxTokens; got != want {
		t.Errorf("retry reasoning budget %d, want %d: the rest is the summary's", got, want)
	}
	if !strings.Contains(l.Compacted.Text(), "- x") {
		t.Errorf("summary = %q, want the retry's", l.Compacted.Text())
	}
}

// thinkerProvider reasons until the cap cuts it off unless the cap leaves
// room, as a model that ignores "no reasoning" does.
type thinkerProvider struct{ reqs []model.Request }

func (p *thinkerProvider) Stream(_ context.Context, req model.Request) (<-chan model.Event, error) {
	p.reqs = append(p.reqs, req)
	ch := make(chan model.Event, 3)
	ch <- model.Event{Kind: model.EventReasoningDelta, Reasoning: "let me think at length"}
	if req.MaxTokens >= 8192 {
		ch <- model.Event{Kind: model.EventTextDelta, Text: "## Goal\n- thought first"}
		ch <- model.Event{Kind: model.EventDone}
	} else {
		ch <- model.Event{Kind: model.EventDone, Truncated: true}
	}
	close(ch)
	return ch, nil
}

func TestSummaryRetriesWhenReasoningFillsTheCap(t *testing.T) {
	p := &thinkerProvider{}
	var done CompactEvent
	l := &Loop{Provider: p, ContextWindow: 16384, History: turns(12), OnCompact: func(e CompactEvent) { done = e }}
	if err := l.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(p.reqs) != 2 || done.Err != nil || !strings.Contains(l.Compacted.Text(), "- thought first") {
		t.Errorf("%d requests, err %v, summary %q", len(p.reqs), done.Err, l.Compacted.Summary)
	}
}

func TestCapSummary(t *testing.T) {
	long := strings.Repeat("- a point about the work so far\n", 400)
	got := capSummary(long, 200)
	if n := tokencount.Count(got); n > 220 {
		t.Errorf("capped summary is %d tokens, want about 200", n)
	}
	if !strings.HasSuffix(got, "- a point about the work so far\n(summary cut at its length limit)") {
		t.Errorf("not cut at a whole line: %q", got[len(got)-80:])
	}
	if short := "## Goal\n- x"; capSummary(short, 200) != short {
		t.Error("short summary changed")
	}
}

// billedProvider replies like replyProvider and reports usage.
type billedProvider struct{ replyProvider }

func (p *billedProvider) Stream(ctx context.Context, req model.Request) (<-chan model.Event, error) {
	in, err := p.replyProvider.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan model.Event, 2)
	for e := range in {
		if e.Kind == model.EventDone {
			e.Usage = &model.Usage{PromptTokens: 9000, CompletionTokens: 500, Cost: 0.25, CostReported: true}
		}
		out <- e
	}
	close(out)
	return out, nil
}

// A compaction's request is paid for like any other: it counts in the
// session's totals.
func TestCompactCountsUsage(t *testing.T) {
	p := &billedProvider{replyProvider{replies: []string{"## Goal\n- read files"}}}
	l := &Loop{Provider: p, ContextWindow: 16384, History: turns(12)}
	if err := l.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if l.stats.SessionPromptTokens != 9000 || l.stats.SessionCompletionTokens != 500 || l.stats.SessionCost != 0.25 {
		t.Errorf("stats after a compaction = %+v", l.stats)
	}
}
