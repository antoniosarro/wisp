package cli

import (
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

// repoURL names wisp to OpenRouter (app attribution).
const repoURL = "https://github.com/antoniosarro/wisp"

// Config holds what the flags set.
type Config struct {
	ModelName       string
	BaseURL         string
	APIKey          string
	Provider        string // OpenRouter upstream provider to pin, from --provider
	Cheapest        bool   // OpenRouter: route to the model's two cheapest providers, from --cheapest
	SkipPermissions bool
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

// newLoop wires the provider and the permission-gated built-in tools into
// a Loop configured for info, the provider's current model. vision is the
// read tool's image switch, which applyModel sets from info.
func newLoop(cfg Config, provider *openaicompat.Client, info model.Info, vision *atomic.Bool, prompter permission.Prompter) (*core.Loop, error) {
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
		return nil, err
	}
	loop := &core.Loop{
		Provider:      provider,
		Tools:         tool.NewRegistry(tools...),
		System:        prompt.Build(workDir, time.Now()),
		FinishCheck:   builtin.TodoReminder,
		MaxIterations: cfg.MaxIterations,
	}
	applyModel(loop, vision, info)
	return loop, nil
}
