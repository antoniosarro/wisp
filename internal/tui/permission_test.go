package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/testutil"
	"github.com/antoniosarro/wisp/internal/tool"
)

func TestPrompterTurnCancellation(t *testing.T) {
	ch := make(chan tea.Msg, 1)
	p := NewPrompter(func(m tea.Msg) { ch <- m }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan permission.Decision, 1)
	go func() { result <- p.PromptContext(ctx, "bash", nil) }()
	mustRecv(t, ch)
	cancel()
	if mustRecvBool(t, result) {
		t.Fatal("canceled turn approved")
	}
}

func TestPrompterAllow(t *testing.T) {
	sendCh := make(chan tea.Msg, 1)
	p := NewPrompter(func(m tea.Msg) { sendCh <- m }, nil)

	resultCh := make(chan permission.Decision, 1)
	go func() { resultCh <- p.Prompt("bash", json.RawMessage(`{"command":"ls"}`)) }()

	msg := mustRecv(t, sendCh)
	req, ok := msg.(PermissionRequestMsg)
	if !ok {
		t.Fatalf("msg = %+v, want a PermissionRequestMsg", msg)
	}
	if req.Name != "bash" || string(req.Args) != `{"command":"ls"}` {
		t.Errorf("req = %+v", req)
	}
	req.Reply <- Answer{Decision: permission.Allow}

	if got := mustRecvBool(t, resultCh); !got {
		t.Error("Prompt returned false, want true")
	}
}

func TestPrompterDeny(t *testing.T) {
	sendCh := make(chan tea.Msg, 1)
	p := NewPrompter(func(m tea.Msg) { sendCh <- m }, nil)

	resultCh := make(chan permission.Decision, 1)
	go func() { resultCh <- p.Prompt("bash", nil) }()

	req := mustRecv(t, sendCh).(PermissionRequestMsg)
	req.Reply <- Answer{Decision: permission.Deny}

	if got := mustRecvBool(t, resultCh); got {
		t.Error("Prompt returned true, want false")
	}
}

func TestPrompterDeniedWhenProgramShutsDown(t *testing.T) {
	sendCh := make(chan tea.Msg, 1)
	done := make(chan struct{})
	p := NewPrompter(func(m tea.Msg) { sendCh <- m }, done)

	resultCh := make(chan permission.Decision, 1)
	go func() { resultCh <- p.Prompt("bash", nil) }()

	mustRecv(t, sendCh) // request sent, nobody answers it
	close(done)

	if got := mustRecvBool(t, resultCh); got {
		t.Error("Prompt returned true after shutdown, want false (deny)")
	}
}

func TestPrompterConcurrentRequestsGetDistinctReplyChannels(t *testing.T) {
	sendCh := make(chan tea.Msg, 2)
	p := NewPrompter(func(m tea.Msg) { sendCh <- m }, nil)

	result1 := make(chan permission.Decision, 1)
	result2 := make(chan permission.Decision, 1)
	go func() { result1 <- p.Prompt("bash", json.RawMessage(`"a"`)) }()
	go func() { result2 <- p.Prompt("write", json.RawMessage(`"b"`)) }()

	req1 := mustRecv(t, sendCh).(PermissionRequestMsg)
	req2 := mustRecv(t, sendCh).(PermissionRequestMsg)
	if req1.Reply == req2.Reply {
		t.Fatal("concurrent requests share a Reply channel")
	}

	// answer them in reverse order to prove replies route back correctly
	req2.Reply <- Answer{Decision: permission.Allow}
	req1.Reply <- Answer{Decision: permission.Deny}

	byName := map[string]bool{
		req1.Name: mustRecvBool(t, result1),
		req2.Name: mustRecvBool(t, result2),
	}
	if byName["bash"] != false || byName["write"] != true {
		t.Errorf("results = %+v, want bash=false write=true", byName)
	}
}

func mustRecv(t *testing.T, ch <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a message")
		return nil
	}
}

// mustRecvBool reports whether the decision received on ch allows the call.
func mustRecvBool(t *testing.T, ch <-chan permission.Decision) bool {
	t.Helper()
	select {
	case v := <-ch:
		return v != permission.Deny
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Prompt's result")
		return false
	}
}

func TestLineDiffShowsOnlyChanges(t *testing.T) {
	var old []string
	for i := range 20 {
		old = append(old, fmt.Sprintf("line %d", i))
	}
	updated := append([]string(nil), old...)
	updated[10] = "changed"
	diff, ok := lineDiff(strings.Join(old, "\n"), strings.Join(updated, "\n"))
	got := stripANSI(diff)
	if !ok || !strings.Contains(got, "- line 10") || !strings.Contains(got, "+ changed") || strings.Contains(got, "line 2\n") || !strings.Contains(got, "  line 8") {
		t.Fatalf("diff =\n%s", got)
	}
}

// request queues an approval for a bash command on m, as the prompter
// would, and returns where its answer goes.
func request(m *Model, command string) <-chan Answer {
	reply := make(chan Answer, 1)
	m.Update(PermissionRequestMsg{Context: context.Background(), Name: "bash", Args: json.RawMessage(`{"command":"` + command + `"}`), Reply: reply})
	return reply
}

// settled makes the front prompt answerable: shown and not typed over
// for longer than approvalGuard.
func settled(m *Model) {
	m.pendingSince = time.Now().Add(-time.Second)
	m.lastKey = time.Time{}
}

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestApprovalAnswers(t *testing.T) {
	for r, want := range map[rune]permission.Decision{'y': permission.Allow, 'a': permission.AllowAlways, 'n': permission.Deny, 'Y': permission.Allow} {
		m, _ := newTestModel(t, &testutil.ScriptedProvider{})
		reply := request(m, "ls")
		if view := stripANSI(m.View()); !strings.Contains(view, "Permission required") || !strings.Contains(view, "$ ls") || !strings.Contains(view, "always allow ls commands") {
			t.Fatalf("approval box:\n%s", view)
		}
		settled(m)
		m.Update(runeKey(r))
		select {
		case a := <-reply:
			if a.Decision != want {
				t.Errorf("%q answered %v, want %v", r, a.Decision, want)
			}
		default:
			t.Fatalf("%q did not answer", r)
		}
		if len(m.pending) != 0 || strings.Contains(stripANSI(m.View()), "Permission required") {
			t.Errorf("%q left the prompt up", r)
		}
	}
}

// A prompt that appears mid-sentence must not take the next letter as an
// answer: it goes to the draft.
func TestApprovalIgnoresHastyKeys(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	reply := request(m, "rm -rf build")

	m.Update(runeKey('y')) // the prompt just appeared
	settled(m)
	m.lastKey = time.Now() // and the user is typing
	m.Update(runeKey('y'))
	select {
	case a := <-reply:
		t.Fatalf("a hasty key answered %v", a.Decision)
	default:
	}
	if m.input.Value() != "yy" {
		t.Errorf("draft = %q, want the hasty keys kept", m.input.Value())
	}
}

func TestApprovalQueue(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	first, second := request(m, "ls"), request(m, "pwd")
	if !strings.Contains(stripANSI(m.View()), "1 of 2") {
		t.Fatalf("queue count missing:\n%s", stripANSI(m.View()))
	}
	settled(m)
	m.Update(runeKey('n'))
	if a := <-first; a.Decision != permission.Deny {
		t.Errorf("first = %v, want Deny", a.Decision)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "$ pwd") || strings.Contains(view, "1 of") {
		t.Errorf("the second request is not shown alone:\n%s", view)
	}
	settled(m)
	m.Update(runeKey('y'))
	if a := <-second; a.Decision != permission.Allow {
		t.Errorf("second = %v, want Allow", a.Decision)
	}
}

func TestApprovalDenyWithNote(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.input.SetValue("my draft")
	reply := request(m, "rm -rf build")
	settled(m)

	m.Update(runeKey('t'))
	if !m.noting || m.input.Value() != "" {
		t.Fatalf("t: noting = %v, input = %q; want an empty note input", m.noting, m.input.Value())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("use make clean")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if a := <-reply; a.Decision != permission.Deny || a.Note != "use make clean" {
		t.Errorf("answer = %+v, want Deny with the note", a)
	}
	if m.noting || m.input.Value() != "my draft" {
		t.Errorf("after the note: noting = %v, input = %q; want the draft back", m.noting, m.input.Value())
	}
}

func TestApprovalNoteEscGoesBack(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	reply := request(m, "ls")
	settled(m)
	m.Update(runeKey('t'))
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	select {
	case a := <-reply:
		t.Fatalf("Esc from the note answered %v", a.Decision)
	default:
	}
	if m.noting || len(m.pending) != 1 {
		t.Errorf("noting = %v, pending = %d; want the prompt back", m.noting, len(m.pending))
	}
}

func TestEscCancelsTurnDenyingApprovals(t *testing.T) {
	m, ch := newTestModel(t, blockingProvider{})
	typeText(m, "hi")
	reply := request(m, "ls")

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a := <-reply; a.Decision != permission.Deny {
		t.Errorf("answer = %v, want Deny", a.Decision)
	}
	if len(m.pending) != 0 {
		t.Error("the prompt is still up")
	}
	pump(t, m, ch)
}

// A request whose call was cancelled while it waited, e.g. with its
// sub-agent, goes away without an answer.
func TestApprovalPrunesCancelledRequests(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.inTurn = true
	ctx, cancel := context.WithCancel(context.Background())
	m.Update(PermissionRequestMsg{Context: ctx, Name: "bash", Args: json.RawMessage(`{}`), Reply: make(chan Answer, 1)})
	cancel()
	m.Update(m.spin.Tick())
	if len(m.pending) != 0 {
		t.Error("a cancelled request is still pending")
	}

	// One already cancelled when it arrives is never shown.
	m.Update(PermissionRequestMsg{Context: ctx, Name: "bash", Args: json.RawMessage(`{}`), Reply: make(chan Answer, 1)})
	if len(m.pending) != 0 {
		t.Error("a request cancelled before it arrived was queued")
	}
}

// A denial marks the call's card at once, and the result that follows
// still reaches it with the user's note.
func TestDenialMarksCallAndKeepsNote(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.blocks.appendEvent(model.Event{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)}})
	reply := make(chan Answer, 1)
	m.Update(PermissionRequestMsg{Context: context.Background(), CallID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`), Reply: reply})
	settled(m)
	m.Update(runeKey('n'))
	<-reply

	if m.blocks[0].toolStatus != toolDenied || !strings.Contains(stripANSI(m.View()), "denied") {
		t.Fatalf("after n the card is %v", m.blocks[0].toolStatus)
	}
	m.Update(ToolResultMsg{Call: model.ToolCall{ID: "c1"}, Result: tool.Result{IsError: true, Content: permission.DeniedContent + ". use ls -la"}})
	if b := m.blocks[0]; b.toolStatus != toolDenied || b.toolResult != "use ls -la" {
		t.Errorf("card = %v %q, want denied with the note", b.toolStatus, b.toolResult)
	}
}

// riskyTool always asks before it runs.
type riskyTool struct{ testutil.EchoTool }

func (riskyTool) Risky() bool { return true }

// The whole path: a gated call asks the UI through the Prompter, which
// waits for the key that answers it.
func TestPrompterThroughGate(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventToolCall, ToolCall: &model.ToolCall{ID: "c1", Name: "echo", Args: json.RawMessage(`{"x":1}`)}}, {Kind: model.EventDone}},
		{{Kind: model.EventTextDelta, Text: "ran it"}, {Kind: model.EventDone}},
	}}
	ch := make(chan tea.Msg, 64)
	send := func(msg tea.Msg) { ch <- msg }
	loop := &core.Loop{Provider: p, Tools: tool.NewRegistry(permission.Gate{Tool: riskyTool{}, Prompter: NewPrompter(send, nil)})}
	m := NewModel(context.Background(), loop, send, ch, Options{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	typeText(m, "go")
	for len(m.pending) == 0 {
		m.Update(mustRecv(t, ch))
	}
	settled(m)
	m.Update(runeKey('y'))
	pump(t, m, ch)

	if view := stripANSI(m.View()); !strings.Contains(view, "✓ echo") || !strings.Contains(view, "ran it") {
		t.Errorf("after allowing:\n%s", view)
	}
}
