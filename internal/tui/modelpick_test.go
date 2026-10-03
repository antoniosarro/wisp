package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

// catalogProvider is a scripted provider that also serves a model list.
type catalogProvider struct {
	testutil.ScriptedProvider
	models  []model.Info
	current string
}

func (p *catalogProvider) Models(context.Context) ([]model.Info, error) { return p.models, nil }
func (p *catalogProvider) Model() string                                { return p.current }
func (p *catalogProvider) SetModel(id string)                           { p.current = id }
func (p *catalogProvider) Describe(_ context.Context, id string) (model.Info, error) {
	for _, m := range p.models {
		if m.ID == id {
			return m, nil
		}
	}
	return model.Info{ID: id}, nil
}

func TestStartupPickerThenSwitch(t *testing.T) {
	p := &catalogProvider{models: []model.Info{
		{ID: "small", ContextWindow: 8192},
		{ID: "big", ContextWindow: 131072, Tools: model.Supported, Vision: model.Supported},
	}}
	loop := &core.Loop{Provider: p, SessionID: "s"}
	var applied model.Info
	opts := Options{Models: p.models, OnModel: func(l *core.Loop, info model.Info) model.Info {
		l.ContextWindow, applied = info.ContextWindow, info
		return info
	}}
	m := NewModel(context.Background(), loop, func(tea.Msg) {}, nil, opts)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	if got := stripANSI(m.View()); !strings.Contains(got, "Choose a model for this session") || !strings.Contains(got, "big    128K context · tools · vision") {
		t.Fatalf("startup picker:\n%s", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // close it to type
	m.input.SetValue("hello")
	m.submit()
	if m.inTurn || m.input.Value() != "hello" {
		t.Fatal("a prompt was sent before a model was chosen")
	}

	m.input.SetValue("/model 2")
	cmd := m.submit()
	msg := cmd().(modelsMsg)
	describe := m.handleModels(msg)
	if p.current != "big" || m.opts.Model.ID != "big" {
		t.Fatalf("current = %q, shown %q; want big selected at once", p.current, m.opts.Model.ID)
	}
	m.Update(describe())
	if applied.ID != "big" || loop.ContextWindow != 131072 || m.opts.Model.Tools != model.Supported {
		t.Fatalf("applied = %+v, loop window %d", applied, loop.ContextWindow)
	}
	if !strings.Contains(transcriptText(m), "Using model") {
		t.Error("no notice about the new model")
	}
}

func TestUnsupportedToolsAreExplained(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.opts.Model.ID = "fast-agent"
	m.useModel(modelInfoMsg{info: model.Info{ID: "fast-agent", Tools: model.Unsupported}})
	if !strings.Contains(transcriptText(m), "cannot call tools") {
		t.Fatalf("render = %q", transcriptText(m))
	}
}

// A description that arrives during a turn waits for it to end: the loop
// reads the model's settings while it runs.
func TestModelDescriptionWaitsForTheTurn(t *testing.T) {
	m, ch := newTestModel(t, blockingProvider{})
	var applied []string
	m.opts.OnModel = func(_ *core.Loop, info model.Info) model.Info {
		applied = append(applied, info.ID)
		return info
	}
	typeText(m, "hi")
	m.Update(modelInfoMsg{info: model.Info{ID: "test-model", ContextWindow: 4096}, quiet: true})
	if len(applied) != 0 {
		t.Fatal("the description was applied mid-turn")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	pump(t, m, ch)
	if len(applied) != 1 || m.opts.Model.ContextWindow != 4096 {
		t.Errorf("after the turn: applied %v, model %+v", applied, m.opts.Model)
	}
}

// Ollama and LM Studio report the context only once a turn has loaded the
// model: the first turn that ends with none known asks again, once.
func TestModelRedescribedAfterFirstTurn(t *testing.T) {
	p := &catalogProvider{ScriptedProvider: testutil.ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventTextDelta, Text: "a"}, {Kind: model.EventDone}},
		{{Kind: model.EventTextDelta, Text: "b"}, {Kind: model.EventDone}},
	}}, models: []model.Info{{ID: "test-model", ContextWindow: 32768}}}
	m, ch := newTestModel(t, p)
	m.opts.OnModel = func(l *core.Loop, info model.Info) model.Info {
		l.ContextWindow = info.ContextWindow
		return info
	}

	typeText(m, "one")
	var redescribe tea.Cmd
	for redescribe == nil {
		msg := mustRecv(t, ch)
		_, cmd := m.Update(msg)
		if _, done := msg.(TurnDoneMsg); done {
			redescribe = cmd
		}
	}
	// The batch holds the listener too: run the describe command alone.
	m.Update(m.describeModel("test-model", true)())
	if m.loop.ContextWindow != 32768 || !m.redescribed {
		t.Fatalf("window %d after the first turn", m.loop.ContextWindow)
	}
	if got := transcriptText(m); !strings.Contains(got, "Using model") || !strings.Contains(got, "32K context") {
		t.Errorf("a refresh that learned the window wasn't announced:\n%s", transcriptText(m))
	}
	if got := transcriptText(m); strings.Index(got, "Using model") > strings.Index(got, "one") {
		t.Errorf("the refresh's notice isn't above the turn it describes:\n%s", got)
	}
	// The second turn knows the window: no more asking.
	typeText(m, "two")
	for {
		msg := mustRecv(t, ch)
		_, cmd := m.Update(msg)
		if _, done := msg.(TurnDoneMsg); done {
			if n := len(cmd().(tea.BatchMsg)); n != 2 {
				t.Errorf("the second turn's end issued %d commands, want the listener and focus only", n)
			}
			break
		}
	}
}

func TestModelNotFound(t *testing.T) {
	p := &catalogProvider{models: []model.Info{{ID: "a"}}}
	m, _ := newTestModel(t, p)
	m.Update(m.handleModels(modelsMsg{models: p.models, selection: "b"}))
	if p.current != "" || !strings.Contains(transcriptText(m), "Model not found: b") {
		t.Errorf("current %q, transcript:\n%s", p.current, transcriptText(m))
	}
}
