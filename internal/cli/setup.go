package cli

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antoniosarro/wisp/internal/agent"
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
	Suggest         bool   // from --suggest: the TUI proposes a next message after each reply
	TraceURL        string // where --trace serves the trace page, shown on the TUI's splash
	ContextWindow   int    // from --context-window; overrides what the endpoint reports
	MaxAgents       int    // from --max-agents: sub-agents running at once
	NoSummarize     bool   // from --no-summarize: masking only
	// TrustProject says the project's .wisp config (MCP servers) may be
	// used: --trust-project, or the user's answer (trust.go).
	TrustProject  bool
	MaxIterations int
	Vision        bool
	Price         model.Pricing  // from --price; overrides what the endpoint reports
	Effort        string         // from --effort: the reasoning effort level, "" for the model's default
	FetchAllow    []netip.Prefix // from --fetch-allow: non-public addresses fetch may reach
	SearchURL     string         // from --search-url: the web_search endpoint; "" for no web_search
	SearchKey     string         // $WISP_SEARCH_KEY: Brave Search's key
	Notify        string         // from --notify: tui.NotifyOff, NotifyBell, or NotifyDesktop
}

// newProvider is the client for cfg's endpoint and model.
func newProvider(cfg Config) *openaicompat.Client {
	return openaicompat.New(openaicompat.Config{
		BaseURL: cfg.BaseURL, Model: cfg.ModelName, APIKey: cfg.APIKey, Provider: cfg.Provider, Cheapest: cfg.Cheapest,
		AppName: "Wisp@" + version.Version, AppURL: repoURL,
	}, nil)
}

// builtinTools are the tools wisp itself provides for cfg. vision is the
// read tool's image switch.
func builtinTools(cfg Config, vision *atomic.Bool) []tool.Tool {
	builtins := []tool.Tool{
		builtin.ReadTool{Vision: vision},
		builtin.LsTool{},
		builtin.GlobTool{},
		builtin.GrepTool{},
		builtin.TodoTool{},
		builtin.WriteTool{},
		builtin.EditTool{},
		builtin.MultiEditTool{},
		builtin.BashTool{},
		builtin.FetchTool{Client: builtin.NewFetchClient(cfg.FetchAllow)},
	}
	// Without an endpoint web_search doesn't exist: no schema in every
	// request, and no call that can only fail.
	if cfg.SearchURL != "" {
		builtins = append(builtins, builtin.WebSearchTool{Endpoint: cfg.SearchURL, Key: cfg.SearchKey})
	}
	return builtins
}

// newLoop wires the provider, the permission-gated built-in and MCP
// tools, sub-agents, and the session store into a Loop configured for
// info, the provider's current model: a new session, or the one cfg
// resumes, from its latest summary. vision is the image switch of the
// read tool and MCP results, which applyModel sets from info. With
// cfg.SkipPermissions, calls run without asking prompter, except
// destructive MCP tools. onAgent receives sub-agent progress, and onTrace,
// if set, every step of their conversations. The caller
// must call cleanup, which closes the store and stops MCP servers.
func newLoop(cfg Config, provider *openaicompat.Client, info model.Info, vision *atomic.Bool, prompter permission.Prompter, onAgent func(agent.Event), onTrace func(agent.Trace)) (loop *core.Loop, cleanup func(), err error) {
	ask := prompter // still asked for destructive MCP tools when skipping permissions
	if cfg.SkipPermissions {
		prompter = permission.AllowAll{}
	}
	tools := permission.GateAll(prompter, builtinTools(cfg, vision)...)
	workDir, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	mcpServers, mcpTools, err := connectMCP(workDir, cfg.TrustProject, vision, prompter, ask)
	if err != nil {
		return nil, nil, err
	}
	tools = append(tools, mcpTools...) // sub-agents share them, and the servers
	mainInfo := func() model.Info {    // read when a sub-agent starts, so it follows the main model
		if loop == nil {
			return model.Info{}
		}
		return model.Info{ContextWindow: loop.ContextWindow, MaxOutput: loop.MaxOutput, Price: loop.Price, Tools: toolSupport(loop)}
	}
	agentTool, err := newAgentTool(cfg, provider, mainInfo, workDir, tools, mcpServers, onAgent, onTrace)
	if err != nil {
		mcpServers.Close()
		return nil, nil, err
	}
	if agentTool != nil {
		// Gated for argument validation; sub-agents' own risky calls still ask.
		tools = append(tools, permission.Gate{Tool: agentTool, Prompter: prompter})
	}
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
	loop.Effort = cfg.Effort
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

// toolSupport is whether the main loop's model can call tools, for
// sub-agents that share it.
func toolSupport(loop *core.Loop) model.Support {
	if loop.NoTools {
		return model.Unsupported
	}
	return model.SupportUnknown
}

// sharesMain reports whether a sub-agent runs on the main provider, and so
// follows the main model.
func sharesMain(s agent.Spec) bool {
	return s.Model == "" && s.BaseURL == "" && s.APIKeyEnv == ""
}

// ownModel is a sub-agent's own provider and, once looked up, what its
// endpoint says about its model.
type ownModel struct {
	provider *openaicompat.Client
	once     sync.Once
	info     model.Info
}

// newAgentTool loads sub-agents from the global and project agent
// directories; it returns nil when none are configured. Agents that don't
// set their own model or endpoint share main's provider. An untrusted
// project's agents can't choose an endpoint or key, and main's key goes
// only to main's host. MCP tools, if any, are among tools and share main's
// servers.
func newAgentTool(cfg Config, main *openaicompat.Client, mainInfo func() model.Info, workDir string, tools []tool.Tool, mcpServers *mcp.Manager, onAgent func(agent.Event), onTrace func(agent.Trace)) (*agent.Tool, error) {
	dirs := []string{projectPath(workDir, "agents")}
	if global := configPath("agents"); global != "" {
		dirs = append([]string{global}, dirs...)
	}
	specs, err := agent.Load(dirs...)
	if err != nil || len(specs) == 0 {
		return nil, err
	}
	if !cfg.TrustProject {
		for i, s := range specs {
			if filepath.Dir(s.Path) == dirs[len(dirs)-1] {
				specs[i].BaseURL, specs[i].APIKeyEnv = "", ""
			}
		}
	}
	own := map[string]*ownModel{} // sub-agents with their own model, by name
	return agent.NewTool(agent.Options{
		Specs: specs,
		Tools: tool.NewRegistry(tools...),
		NewProvider: func(s agent.Spec) model.Provider {
			if sharesMain(s) {
				return main
			}
			var key string
			switch {
			case s.APIKeyEnv != "":
				key = os.Getenv(s.APIKeyEnv)
			case s.BaseURL == "" || sameHost(s.BaseURL, cfg.BaseURL):
				key = cfg.APIKey
			}
			// Not pinned: --provider is for main's model, which another
			// provider may not serve.
			p := newProvider(Config{BaseURL: cmp.Or(s.BaseURL, cfg.BaseURL), ModelName: cmp.Or(s.Model, main.Model()), APIKey: key, Cheapest: cfg.Cheapest})
			own[s.Name] = &ownModel{provider: p}
			return p
		},
		Info: func(s agent.Spec) model.Info {
			o, ok := own[s.Name]
			if !ok {
				return mainInfo()
			}
			o.once.Do(func() {
				ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
				defer cancel()
				if info, err := o.provider.Describe(ctx, o.provider.Model()); err == nil {
					o.info = info
				}
			})
			return o.info
		},
		System: func(s agent.Spec) string {
			system := prompt.BuildAgent(s.Instructions, workDir, time.Now())
			if len(mcpServers.Servers()) > 0 && (len(s.Tools) == 0 || slices.Contains(s.Tools, mcp.CallToolName)) {
				system += "\n\n" + mcp.Prompt(mcpServers)
			}
			return system
		},
		MaxParallel: cfg.MaxAgents,
		Observer:    onAgent,
		Trace:       onTrace,
	})
}
