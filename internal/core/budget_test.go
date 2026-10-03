package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

// budgetProvider answers each request after the scripted errors with
// "ok", reporting prompt tokens as ratio × the local estimate of what
// was sent.
type budgetProvider struct {
	errs  []error
	ratio float64
	reqs  []model.Request
}

func (p *budgetProvider) Stream(_ context.Context, req model.Request) (<-chan model.Event, error) {
	p.reqs = append(p.reqs, req)
	if len(p.reqs) <= len(p.errs) {
		return nil, p.errs[len(p.reqs)-1]
	}
	est := 0
	for _, m := range req.Messages {
		est += messageTokens(m)
	}
	ch := make(chan model.Event, 2)
	ch <- model.Event{Kind: model.EventTextDelta, Text: "ok"}
	ch <- model.Event{Kind: model.EventDone, Usage: &model.Usage{PromptTokens: int(float64(est) * p.ratio)}}
	close(ch)
	return ch, nil
}

// readHistory is a finished turn that read n files, each result big.
func readHistory(n int, big string) []model.Message {
	h := []model.Message{{Role: model.RoleUser, Content: "look"}}
	for i := range n {
		id := fmt.Sprint("r", i)
		h = append(h,
			model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: id, Name: "read", Args: json.RawMessage(fmt.Sprintf(`{"path":"f%d.go"}`, i))}}},
			model.Message{Role: model.RoleTool, ToolCallID: id, Content: big},
		)
	}
	return append(h, model.Message{Role: model.RoleAssistant, Content: "seen"})
}

func TestMessageTokensCountsToolCallArguments(t *testing.T) {
	body := strings.Repeat("func f() {}\n", 300)
	args, _ := json.Marshal(map[string]string{"path": "f.go", "content": body})
	bare := messageTokens(model.Message{Role: model.RoleAssistant})
	withCall := messageTokens(model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{Name: "write", Args: args}}})
	if withCall-bare < 1000 {
		t.Errorf("a write of %d bytes adds %d tokens", len(body), withCall-bare)
	}
	withImage := messageTokens(model.Message{Role: model.RoleTool, Images: []model.Image{{MIME: "image/png"}}})
	if withImage < imageTokens {
		t.Errorf("an image counts %d tokens", withImage)
	}
}

// A History replaced by another slice, even of the same length, must be
// counted afresh; one that grew counts only what was added.
func TestHistoryTokensFollowsHistory(t *testing.T) {
	big := strings.Repeat("some line of source code\n", 100)
	l := &Loop{History: readHistory(3, big)}
	full := l.historyTokens()
	l.History = readHistory(3, "x")
	small := l.historyTokens()
	if small >= full {
		t.Errorf("replaced history counts %d tokens, the old one's %d", small, full)
	}
	extra := model.Message{Role: model.RoleUser, Content: big}
	l.History = append(l.History, extra)
	if got := l.historyTokens(); got != small+messageTokens(extra) {
		t.Errorf("grown history counts %d, want %d + %d", got, small, messageTokens(extra))
	}
}

// Dropping an earlier turn's image changes an old message: the count must
// start over, not keep the image's cost.
func TestDroppedImagesAreCountedAgain(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "ok"}, {Kind: model.EventDone}}}}
	l := &Loop{Provider: p, History: []model.Message{
		{Role: model.RoleUser, Content: "look"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "1", Name: "read"}}},
		{Role: model.RoleTool, ToolCallID: "1", Content: "a.png", Images: []model.Image{{MIME: "image/png"}}},
		{Role: model.RoleAssistant, Content: "a cat"},
	}}
	before := l.historyTokens()
	if _, err := l.Run(context.Background(), "and now?"); err != nil {
		t.Fatal(err)
	}
	if got := l.historyTokens(); got >= before {
		t.Errorf("history %d tokens after dropping the image, %d before", got, before)
	}
}

func TestCalibrate(t *testing.T) {
	l := &Loop{}
	if l.ratio() != 1 {
		t.Fatalf("uncalibrated ratio = %v", l.ratio())
	}
	l.calibrate(1200, 1000)
	if l.ratio() != 1.2 {
		t.Errorf("first sample: ratio = %v, want 1.2", l.ratio())
	}
	l.calibrate(1000, 1000)
	if want := 1.2 + ratioAlpha*(1-1.2); math.Abs(l.ratio()-want) > 1e-9 {
		t.Errorf("second sample: ratio = %v, want %v", l.ratio(), want)
	}
	l.calibrate(0, 1000) // no usage: ignored
	l.calibrate(9000, 1000)
	if l.ratio() > ratioMax {
		t.Errorf("ratio %v past the clamp", l.ratio())
	}
}

// Each response's reported prompt size calibrates the estimates.
func TestRunCalibrates(t *testing.T) {
	p := &budgetProvider{ratio: 1.3}
	l := &Loop{Provider: p, System: "be brief"}
	if _, err := l.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if math.Abs(l.ratio()-1.3) > 0.05 {
		t.Errorf("ratio = %v after one request, want about 1.3", l.ratio())
	}
}

func TestHistoryBudget(t *testing.T) {
	system := strings.Repeat("word ", 1000)
	l := &Loop{System: system}
	if got := l.historyBudget(); got != 0 {
		t.Errorf("unknown window: budget = %d, want 0", got)
	}
	l.ContextWindow = 16384
	fixed := l.fixedTokens()
	if got, want := l.historyBudget(), 16384-4096-fixed; got != want {
		t.Errorf("16K window: budget = %d, want %d", got, want)
	}
	l.MaxOutput = 2048
	if got, want := l.historyBudget(), 16384-2048-fixed; got != want {
		t.Errorf("2K max output: budget = %d, want %d", got, want)
	}
	l.ContextWindow = 1000
	if got := l.historyBudget(); got != 1 {
		t.Errorf("window smaller than the fixed cost: budget = %d, want 1", got)
	}
}

// A turn always asks for a bounded reply that fits the window: OpenRouter
// holds credit for the cap, and vLLM refuses prompt plus cap past it.
func TestReplyCap(t *testing.T) {
	l := &Loop{}
	if got := l.replyCap(); got != maxReply {
		t.Errorf("nothing known: cap = %d, want %d", got, maxReply)
	}
	l.MaxOutput = 8000
	if got := l.replyCap(); got != 8000 {
		t.Errorf("8000 max output: cap = %d, want 8000", got)
	}
	l = &Loop{ContextWindow: 1 << 20, MaxOutput: 943717}
	if got := l.replyCap(); got != maxReply {
		t.Errorf("1M window, 943K max output: cap = %d, want %d", got, maxReply)
	}

	big := strings.Repeat("some line of source code\n", 100) // ~600 tokens
	for n := range 25 {
		l := &Loop{ContextWindow: 16384, History: readHistory(n, big)}
		prompt := l.projectedHistory() + l.fixedTokens()
		got := l.replyCap()
		if got < l.reserve() || got > maxReply {
			t.Errorf("%d reads: cap = %d, want within the reserve %d and %d", n, got, l.reserve(), maxReply)
		}
		if got > l.reserve() && prompt+got > l.ContextWindow {
			t.Errorf("%d reads: prompt %d + cap %d is past the window %d", n, prompt, got, l.ContextWindow)
		}
	}
}

func TestTurnRequestsSendTheReplyCap(t *testing.T) {
	p := &budgetProvider{ratio: 1}
	l := &Loop{Provider: p, ContextWindow: 1 << 20}
	if _, err := l.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if got := p.reqs[0].MaxTokens; got != maxReply {
		t.Errorf("max_tokens = %d, want %d", got, maxReply)
	}
}

func TestProjectionUsesCalibratedRatio(t *testing.T) {
	l := &Loop{History: readHistory(5, strings.Repeat("some line of source code\n", 100))}
	local := l.historyTokens()
	l.calibrate(int(float64(l.fixedTokens()+local)*1.4), l.fixedTokens()+local)
	if got, want := l.projectedHistory(), int(float64(local)*1.4); got != want {
		t.Errorf("projected history = %d, want the estimate %d scaled to %d", got, local, want)
	}
}

func TestWindowFromError(t *testing.T) {
	for msg, want := range map[string]int{
		"400 Bad Request: This model's maximum context length is 32768 tokens. However, you requested 33000 tokens":                                          32768,
		`400 Bad Request: {"error":{"message":"request (17000 tokens) exceeds the available context size (16384 tokens), try increasing it","n_ctx":16384}}`: 16384,
		`400: {"n_prompt_tokens":9000,"n_ctx": 8192}`: 8192,
		"400: context length exceeded":                0,
	} {
		if got := windowFromError(errors.New(msg)); got != want {
			t.Errorf("windowFromError(%q) = %d, want %d", msg, got, want)
		}
	}
}

func TestElidesBeforeSendingPastTheTrigger(t *testing.T) {
	big := strings.Repeat("some line of source code\n", 100) // ~600 tokens
	p := &budgetProvider{ratio: 1}
	l := &Loop{Provider: p, ContextWindow: 16384, History: readHistory(15, big)}
	budget := l.historyBudget()
	if l.projectedHistory() <= budget*maskTriggerPct/100 {
		t.Fatalf("history %d doesn't pass the trigger of budget %d", l.projectedHistory(), budget)
	}

	if _, err := l.Run(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	if len(p.reqs) != 1 {
		t.Fatalf("%d requests, want 1: elision must happen before sending", len(p.reqs))
	}
	if got, target := l.projectedHistory(), budget*maskTargetPct/100; got > target {
		t.Errorf("history %d after elision, want at most the target %d", got, target)
	}
	msgs := p.reqs[0].Messages
	if !strings.HasPrefix(msgs[2].Content, "[read f0.go") {
		t.Errorf("oldest result not elided: %.60q", msgs[2].Content)
	}
	if last := msgs[len(msgs)-3]; last.Content != big {
		t.Errorf("newest result elided though the target was reached: %.60q", last.Content)
	}
}

// A rejected request is retried after masking, with the window the server
// states.
func TestOverflowLearnsWindow(t *testing.T) {
	big := strings.Repeat("some line of source code\n", 100)
	overflow := fmt.Errorf("400: This model's maximum context length is 8192 tokens: %w", model.ErrContextOverflow)
	p := &budgetProvider{ratio: 1, errs: []error{overflow}}
	l := &Loop{Provider: p, History: readHistory(15, big)}

	if _, err := l.Run(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	if l.ContextWindow != 8192 {
		t.Errorf("window = %d, want 8192 from the error", l.ContextWindow)
	}
	if n := len(p.reqs); n != 2 {
		t.Errorf("%d requests, want the rejected one and one retry", n)
	}
	// Masked for the stated window, though not down to the target: the
	// last minProtectedSteps reads alone fill most of it.
	if got, trigger := l.projectedHistory(), l.historyBudget()*maskTriggerPct/100; got > trigger {
		t.Errorf("history %d after the retry, want at most %d", got, trigger)
	}
}

// A rejection while History is already under the target means the
// estimate is off: elide everything rather than fail.
func TestOverflowUnderTargetElidesEverything(t *testing.T) {
	big := strings.Repeat("some line of source code\n", 100)
	overflow := fmt.Errorf("400: maximum context length exceeded: %w", model.ErrContextOverflow)
	p := &budgetProvider{ratio: 1, errs: []error{overflow}}
	l := &Loop{Provider: p, ContextWindow: 65536, History: readHistory(15, big)}

	if _, err := l.Run(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	if n := len(p.reqs); n != 2 {
		t.Fatalf("%d requests, want 2", n)
	}
	for _, m := range p.reqs[1].Messages {
		if m.Role == model.RoleTool && !strings.HasPrefix(m.Content, "[read") {
			t.Errorf("result not elided on the last retry: %.60q", m.Content)
		}
	}
}

// An overflow nothing can be masked away from fails the turn, explained.
func TestOverflowWithNothingToMask(t *testing.T) {
	overflow := fmt.Errorf("400: maximum context length exceeded: %w", model.ErrContextOverflow)
	p := &budgetProvider{ratio: 1, errs: []error{overflow}}
	_, err := (&Loop{Provider: p}).Run(context.Background(), "next")
	if !errors.Is(err, model.ErrContextOverflow) || !strings.Contains(err.Error(), "no longer fits") || len(p.reqs) != 1 {
		t.Fatalf("Run = %v after %d requests, want the overflow explained, without retrying", err, len(p.reqs))
	}
}

// bigTool returns n lines of output.
type bigTool struct{ n int }

func (bigTool) Schema() model.ToolSchema { return model.ToolSchema{Name: "big"} }
func (bigTool) Risky() bool              { return false }
func (b bigTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: strings.Repeat("some line of output\n", b.n)}, nil
}

// One tool result can't take more than resultPct of the budget: past it,
// it keeps its head and tail.
func TestToolResultsFitTheBudget(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()) // Clip saves the full output there
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c", Name: "big", Args: json.RawMessage(`{}`)}}, {Kind: model.EventDone}},
		{{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventDone}},
	}}
	l := &Loop{Provider: p, Tools: tool.NewRegistry(bigTool{n: 3000}), ContextWindow: 16384}
	if _, err := l.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	result := l.History[2].Content
	limit := l.historyBudget() * resultPct / 100
	if got := messageTokens(model.Message{Content: result}); got > limit+100 {
		t.Errorf("result is %d tokens, want about the limit %d", got, limit)
	}
	if !strings.Contains(result, "bytes omitted") || !strings.HasPrefix(result, "some line of output") {
		t.Errorf("clipped result lacks its head or the note: %.80q", result)
	}

	// With the window unknown, nothing is clipped here: tools cap their own
	// output.
	if got := (&Loop{}).fitResult(strings.Repeat("x", 100_000)); len(got) != 100_000 {
		t.Errorf("unknown window: result cut to %d bytes", len(got))
	}
}

func TestContextUsageBreakdown(t *testing.T) {
	system := "You are wisp.\n\n# Environment\n- Date: today\n\n# Project instructions (AGENTS.md)\n# Style\nUse tabs.\n\n# MCP servers\nConnected: github."
	prompt, project, mcp := systemSections(system)
	if !strings.HasSuffix(prompt, "- Date: today\n") || !strings.HasPrefix(project, "\n# Project instructions") || !strings.Contains(project, "# Style") || !strings.HasPrefix(mcp, "\n# MCP servers") {
		t.Fatalf("sections = %q | %q | %q", prompt, project, mcp)
	}

	l := &Loop{System: system, ContextWindow: 16384, Tools: tool.NewRegistry(bigTool{}), History: readHistory(3, "x")}
	c := l.ContextUsage()
	if c.Prompt == 0 || c.Project == 0 || c.MCPPrompt == 0 || c.Tools["big"] == 0 || c.Fixed != c.Prompt+c.Project+c.MCPPrompt+c.Tools["big"] {
		t.Errorf("fixed %d != prompt %d + project %d + mcp %d + tools %v", c.Fixed, c.Prompt, c.Project, c.MCPPrompt, c.Tools)
	}
	if c.Messages != c.History || c.History == 0 || c.Reserve != 4096 || c.Budget != 16384-4096-c.Fixed {
		t.Errorf("messages %d, history %d, reserve %d, budget %d", c.Messages, c.History, c.Reserve, c.Budget)
	}
	if (&Loop{}).ContextUsage().Reserve != 0 {
		t.Error("unknown window: a reserve was reported")
	}

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	l.History = readHistory(3, strings.Repeat("some line of source code\n", 100))
	l.mask(0, false, 0)
	c = l.ContextUsage()
	if c.MaskedResults != 3 || c.MaskedSaved < 3*400 {
		t.Errorf("masked %d results saving %d tokens", c.MaskedResults, c.MaskedSaved)
	}
}

func TestStatsReportTheRequestBreakdown(t *testing.T) {
	var got StepStats
	l := &Loop{Provider: &budgetProvider{ratio: 1}, System: "be brief", Tools: tool.NewRegistry(bigTool{}), ContextWindow: 16384,
		OnStats: func(s StepStats) { got = s }}
	if _, err := l.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if got.SystemTokensEst == 0 || got.HistoryTokensEst == 0 || got.ToolDefTokensEst == 0 || got.Context.Window != 16384 {
		t.Errorf("stats = %+v, want the request's parts and the budget", got)
	}
}
