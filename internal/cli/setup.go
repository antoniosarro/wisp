package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/mcp"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/model/openaicompat"
	"github.com/antoniosarro/wisp/internal/permission"
	"github.com/antoniosarro/wisp/internal/prompt"
	"github.com/antoniosarro/wisp/internal/span"
	"github.com/antoniosarro/wisp/internal/termsafe"
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
	// TrustProject says the project's .wisp config (MCP servers) may be
	// used. Until wisp can ask (project trust), it is never set.
	TrustProject  bool
	MaxIterations int
	Vision        bool
	Price         model.Pricing // from --price; overrides what the endpoint reports
}

// newProvider is the client for cfg's endpoint and model.
func newProvider(cfg Config) *openaicompat.Client {
	return openaicompat.New(openaicompat.Config{
		BaseURL: cfg.BaseURL, Model: cfg.ModelName, APIKey: cfg.APIKey, Provider: cfg.Provider, Cheapest: cfg.Cheapest,
		AppName: "Wisp@" + version.Version, AppURL: repoURL,
	}, nil)
}

// newLoop wires the provider, the permission-gated built-in and MCP
// tools, and the session store into a Loop configured for info, the
// provider's current model: a new session, or the one cfg resumes, from
// its latest summary. vision is the image switch of the read tool and MCP
// results, which applyModel sets from info. With cfg.SkipPermissions,
// calls run without asking prompter, except destructive MCP tools. The
// caller must call cleanup, which closes the store and stops MCP servers.
func newLoop(cfg Config, provider *openaicompat.Client, info model.Info, vision *atomic.Bool, prompter permission.Prompter) (loop *core.Loop, cleanup func(), err error) {
	ask := prompter // still asked for destructive MCP tools when skipping permissions
	if cfg.SkipPermissions {
		prompter = permission.AllowAll{}
	}
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
	mcpServers, mcpTools, err := connectMCP(workDir, cfg.TrustProject, vision, prompter, ask)
	if err != nil {
		return nil, nil, err
	}
	tools = append(tools, mcpTools...)
	store, history, sessionID, err := openSession(cfg.ResumeID, info.ID)
	if err != nil {
		mcpServers.Close()
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
	if len(mcpTools) > 0 {
		loop.System += "\n\n" + mcp.Prompt(mcpServers)
	}
	loop.Spans = span.NewRecorder(store, sessionID)
	// The servers started before the session existed: record their starts
	// now, at the top of its trace.
	for _, st := range mcpServers.Starts {
		status, attrs := span.StatusOK, map[string]any{"wisp.mcp.server": st.Server, "wisp.mcp.tools": st.Tools}
		if st.Err != nil {
			status, attrs["error"] = span.StatusError, st.Err.Error()
		}
		loop.Spans.Add(span.KindMCPConnect, st.Server, st.Start, st.End, status, attrs)
	}
	// The window must be set first: summaries are rendered for it.
	applyModel(loop, vision, info)
	if err := loop.LoadCompaction(); err != nil {
		fmt.Fprintf(os.Stderr, "wisp: %v; resuming without the summary\n", err)
	}
	// The recorder writes what's queued before the store closes.
	return loop, func() { loop.StopPresummary(); loop.Spans.Close(); _ = store.Close(); mcpServers.Close() }, nil
}

// mcpConnectTimeout bounds each MCP server's start and tool listing.
const mcpConnectTimeout = 20 * time.Second

// connectMCP starts the servers in the global mcp.json and, when trusted,
// the project's, and returns tool_search and mcp_call over their tools, or
// no tools when no server is connected. Servers that fail to start are
// reported and skipped. Destructive tools ask ask, even when prompter
// allows everything.
func connectMCP(workDir string, trusted bool, vision *atomic.Bool, prompter, ask permission.Prompter) (*mcp.Manager, []tool.Tool, error) {
	var paths []string
	if global := configPath("mcp.json"); global != "" {
		paths = append(paths, global)
	}
	if trusted {
		paths = append(paths, projectPath(workDir, "mcp.json"))
	}
	configs, err := mcp.LoadConfig(paths...)
	if err != nil {
		return nil, nil, err
	}
	if len(configs) > 0 {
		fmt.Fprintf(os.Stderr, "wisp: starting %d MCP server(s)...\n", len(configs))
	}
	start := time.Now()
	m, errs := mcp.Connect(context.Background(), configs, mcpConnectTimeout, vision)
	for _, err := range errs {
		fmt.Fprintf(os.Stderr, "wisp: %v\n", termsafe.Strip(err.Error()))
	}
	if len(m.Servers()) == 0 {
		return m, nil, nil
	}
	fmt.Fprintf(os.Stderr, "wisp: MCP: %s (%d tools) in %s\n", strings.Join(m.Servers(), ", "), len(m.Tools()), time.Since(start).Round(100*time.Millisecond))
	return m, []tool.Tool{mcp.NewSearchTool(m), mcp.NewCallTool(m, mcp.Gate(prompter, ask))}, nil
}
