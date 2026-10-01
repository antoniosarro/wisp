package tui

import (
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

func TestEffortCommand(t *testing.T) {
	p := &testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "ok"}, {Kind: model.EventDone}}}}
	m, ch := newTestModel(t, p)
	m.opts.Model.Efforts = model.EffortsOf("low", "high")

	m.runCommand("/effort")
	pk := m.modal
	if pk == nil {
		t.Fatal("/effort opened no picker")
	}
	var values []string
	for _, it := range pk.items {
		values = append(values, it.value)
	}
	if got := strings.Join(values, " "); got != "default low high" || !pk.items[0].current {
		t.Fatalf("picker = %q, current %v; want default (current), low, high", got, pk.items[0].current)
	}
	m.modal = nil

	m.runCommand("/effort medium")
	if m.loop.Effort != "" || !strings.Contains(transcriptText(m), "doesn't take reasoning effort") {
		t.Fatalf("effort = %q; want medium refused", m.loop.Effort)
	}
	m.runCommand("/effort HIGH")
	if m.loop.Effort != "high" || !strings.Contains(m.statusline(), "test-model (high)") {
		t.Fatalf("effort = %q, statusline %q", m.loop.Effort, m.statusline())
	}
	typeText(m, "hi")
	pump(t, m, ch)
	if p.LastReq.Effort != "high" {
		t.Errorf("request effort = %q, want high", p.LastReq.Effort)
	}
	m.runCommand("/effort default")
	if m.loop.Effort != "" {
		t.Errorf("effort = %q after /effort default", m.loop.Effort)
	}
}

func TestEffortUnreportedOffersOnlyDefault(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.opts.Model.Local = true
	m.runCommand("/effort")
	if pk := m.modal; len(pk.items) != 1 || !strings.Contains(pk.title, "once the model has loaded") {
		t.Fatalf("picker %q has %d items", pk.title, len(pk.items))
	}
	m.runCommand("/effort xhigh")
	if m.loop.Effort != "xhigh" {
		t.Errorf("effort = %q, want xhigh taken on trust", m.loop.Effort)
	}
}

func TestSwitchExplainsDroppedEffort(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.Effort = "max"
	m.opts.OnModel = func(loop *core.Loop, info model.Info) model.Info {
		loop.Effort = "" // as applyModel does for a level the model doesn't take
		return info
	}
	m.useModel(modelInfoMsg{info: model.Info{ID: "test-model", Efforts: model.EffortsOf("none")}})
	if got := transcriptText(m); !strings.Contains(got, "doesn't take reasoning effort  max ") {
		t.Errorf("transcript = %q", got)
	}
}

func TestEffortReportedEmptyOffersOnlyDefault(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.opts.Model.Efforts = model.EffortsReported
	m.runCommand("/effort")
	if pk := m.modal; len(pk.items) != 1 || pk.items[0].value != defaultEffort || strings.Contains(pk.title, "not reported") {
		t.Fatalf("picker %q has %d items, want only default", pk.title, len(pk.items))
	}
	m.runCommand("/effort high")
	if m.loop.Effort != "" {
		t.Errorf("effort = %q, want high refused", m.loop.Effort)
	}
}

// llama-swap reports the context window up front, but a model's effort
// levels only once it runs: the TUI asks again after the first turn.
func TestLocalModelRedescribedForEfforts(t *testing.T) {
	p := &catalogProvider{ScriptedProvider: testutil.ScriptedProvider{Turns: [][]model.Event{{{Kind: model.EventTextDelta, Text: "ok"}, {Kind: model.EventDone}}}}}
	m, ch := newTestModel(t, p)
	m.loop.ContextWindow = 32768
	m.opts.Model = model.Info{ID: "test-model", ContextWindow: 32768, Local: true}
	typeText(m, "hi")
	msg := <-ch
	for ; ; msg = <-ch {
		if _, ok := msg.(TurnDoneMsg); ok {
			break
		}
		m.Update(msg)
	}
	if _, cmd := m.Update(msg); cmd == nil || !m.redescribed {
		t.Fatal("no description asked for after the first turn")
	}
}
