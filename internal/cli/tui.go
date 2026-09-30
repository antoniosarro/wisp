package cli

import (
	"context"
	"os"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/antoniosarro/wisp/internal/agent"
	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/model/openaicompat"
	"github.com/antoniosarro/wisp/internal/session"
	"github.com/antoniosarro/wisp/internal/tui"
)

// runTUI launches the Bubble Tea frontend. The prompter and turn callbacks
// write to msgCh, which the Model reads, so no *tea.Program is needed up front.
func runTUI(ctx context.Context, cfg Config, provider *openaicompat.Client, info model.Info, models []model.Info) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	msgCh := make(chan tea.Msg, 64)
	done := make(chan struct{})
	defer close(done)
	// A send after the program has stopped reading must not block the
	// turn goroutine forever: Wait below waits for it.
	send := func(m tea.Msg) {
		select {
		case msgCh <- m:
		case <-ctx.Done():
		case <-done:
		}
	}

	vision := new(atomic.Bool)
	loop, cleanup, err := newLoop(cfg, provider, info, vision, tui.NewPrompter(send, done),
		func(e agent.Event) { send(tui.AgentMsg(e)) }, func(t agent.Trace) { send(tui.AgentTraceMsg(t)) })
	if err != nil {
		return err
	}
	defer cleanup()

	wd, _ := os.Getwd()
	m := tui.NewModel(ctx, loop, send, msgCh, tui.Options{
		Model:   info,
		Models:  models,
		WorkDir: wd,
		Suggest: cfg.Suggest,
		OnModel: func(loop *core.Loop, info model.Info) model.Info {
			info = cfg.override(info)
			applyModel(loop, vision, info)
			rememberModel(cfg.BaseURL, info.ID)
			if store, ok := loop.Store.(*session.Store); ok {
				_ = store.SetSessionModel(loop.SessionID, info.ID) // the next resume picks it again
			}
			return info
		},
	})
	_, err = tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(tui.NewInput(os.Stdin)), tea.WithAltScreen()).Run()
	cancel() // stop any in-flight turn, then let it record its results
	m.Wait()
	return err
}

// terminalUI says whether stdin and stdout are a terminal the UI can take over.
func terminalUI() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
