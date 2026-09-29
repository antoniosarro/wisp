package cli

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/model/openaicompat"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/prompt"
	"github.com/antoniosarro/wisp/internal/tool"
	"github.com/antoniosarro/wisp/internal/tool/builtin"
	"github.com/antoniosarro/wisp/internal/version"
)

// Config holds what the flags set.
type Config struct {
	ModelName       string
	BaseURL         string
	ResumeID        string // from --resume
	APIKey          string
	Provider        string // OpenRouter upstream provider to pin, from --provider
	Cheapest        bool   // OpenRouter: route to the model's two cheapest providers, from --cheapest
	SkipPermissions bool
	ContextWindow   int  // from --context-window; overrides what the endpoint reports
	NoSummarize     bool // from --no-summarize: masking only
	MaxIterations   int
	Vision          bool
	Price           model.Pricing // from --price; overrides what the endpoint reports
}

// newProvider is the client for cfg's endpoint and model.
func newProvider(cfg Config) *openaicompat.Client {
	return openaicompat.New(openaicompat.Config{
		BaseURL: cfg.BaseURL, Model: cfg.ModelName, APIKey: cfg.APIKey, Provider: cfg.Provider, Cheapest: cfg.Cheapest,
		AppName: "Wisp@" + version.Version, AppURL: repoURL,
	}, nil)
}

// newLoop wires the provider, the permission-gated built-in tools, and the
// session store into a Loop configured for info, the provider's current
// model: a new session, or the one cfg resumes, from its latest summary.
// vision is the read tool's image switch, which applyModel sets from info.
// The caller must call cleanup, which closes the store.
func newLoop(cfg Config, provider *openaicompat.Client, info model.Info, vision *atomic.Bool, prompter permission.Prompter) (loop *core.Loop, cleanup func(), err error) {
	tools := permission.GateAll(prompter,
		builtin.ReadTool{Vision: vision},
		builtin.LsTool{},
		builtin.GlobTool{},
		builtin.GrepTool{},
		builtin.TodoTool{},
		builtin.WriteTool{},
		builtin.EditTool{},
		builtin.MultiEditTool{},
		builtin.BashTool{},
		builtin.FetchTool{},
	)
	workDir, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	store, history, sessionID, err := openSession(cfg.ResumeID, info.ID)
	if err != nil {
		return nil, nil, err
	}
	if cfg.ResumeID != "" && info.ID != "" {
		_ = store.SetSessionModel(sessionID, info.ID) // the next resume picks it again
	}
	fmt.Fprintf(os.Stderr, "wisp: session %s\n", sessionID)

	loop = &core.Loop{
		Provider:      provider,
		Tools:         tool.NewRegistry(tools...),
		System:        prompt.Build(workDir, time.Now()),
		FinishCheck:   builtin.TodoReminder,
		Store:         store,
		SessionID:     sessionID,
		History:       history,
		MaxIterations: cfg.MaxIterations,
		AutoCompact:   !cfg.NoSummarize,
	}
	// The window must be set first: summaries are rendered for it.
	applyModel(loop, vision, info)
	if err := loop.LoadCompaction(); err != nil {
		fmt.Fprintf(os.Stderr, "wisp: %v; resuming without the summary\n", err)
	}
	return loop, func() { loop.StopPresummary(); _ = store.Close() }, nil
}
