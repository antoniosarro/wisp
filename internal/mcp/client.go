package mcp

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antoniosarro/wisp/internal/tool"
	"github.com/antoniosarro/wisp/internal/version"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Manager holds the session's connected servers and their tools. Its
// server list is fixed once Connect returns; each server's tools change
// when it says its list changed.
type Manager struct {
	servers []*server // sorted by name, so the index is stable across runs
	vision  *atomic.Bool

	// Starts times each server's start, failed ones included, for the
	// session's trace.
	Starts []ServerStart

	mu sync.RWMutex // guards each server's tools, which list_changed replaces
}

// ServerStart is how one server's start went.
type ServerStart struct {
	Server     string
	Start, End time.Time
	Tools      int
	Err        error
}

// server is one connected server.
type server struct {
	name         string
	session      *sdk.ClientSession
	instructions string
	tools        []*Tool // sorted by name
}

// Connect starts every configured server in parallel and lists its tools,
// giving each timeout to do so. Servers that fail are left out and
// reported in errs. vision says whether image results reach the model.
func Connect(ctx context.Context, configs map[string]ServerConfig, timeout time.Duration, vision *atomic.Bool) (m *Manager, errs []error) {
	m = &Manager{vision: vision}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, cfg := range configs {
		wg.Go(func() {
			start := time.Now()
			s, err := m.connect(ctx, name, cfg, timeout)
			mu.Lock()
			defer mu.Unlock()
			c := ServerStart{Server: name, Start: start, End: time.Now(), Err: err}
			if s != nil {
				c.Tools = len(s.tools)
			}
			m.Starts = append(m.Starts, c)
			if err != nil {
				errs = append(errs, fmt.Errorf("mcp server %q: %w", name, err))
				return
			}
			m.servers = append(m.servers, s)
		})
	}
	wg.Wait()
	slices.SortFunc(m.servers, func(a, b *server) int { return cmp.Compare(a.name, b.name) })
	return m, errs
}

// connect starts or reaches one server, within timeout, and lists its
// tools. A stdio server gets wisp's environment without its API key, plus
// its own env; an HTTP one gets its headers.
func (m *Manager) connect(ctx context.Context, name string, cfg ServerConfig, timeout time.Duration) (*server, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var transport sdk.Transport
	if cfg.Command != "" {
		// Not CommandContext: the process outlives this connect deadline.
		cmd := exec.Command(cfg.Command, cfg.Args...)
		cmd.Env = tool.ChildEnv()
		for k, v := range cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		transport = &sdk.CommandTransport{Command: cmd}
	} else {
		endpoint, err := url.Parse(cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("url: %w", err)
		}
		transport = &sdk.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: &http.Client{Transport: headerTransport{headers: cfg.Headers, host: endpoint.Host}},
		}
	}
	s := &server{name: name}
	client := sdk.NewClient(&sdk.Implementation{Name: "wisp", Version: version.Version}, &sdk.ClientOptions{
		// Not inline: the handler runs on the connection's reader.
		ToolListChangedHandler: func(_ context.Context, req *sdk.ToolListChangedRequest) {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				_ = m.refresh(ctx, s, req.Session)
			}()
		},
	})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	s.session = session
	if init := session.InitializeResult(); init != nil {
		s.instructions = init.Instructions
	}
	if err := m.refresh(ctx, s, session); err != nil {
		_ = session.Close()
		return nil, err
	}
	return s, nil
}

// refresh relists s's tools. Tools that stay keep their *Tool, so loaded
// ones pick up schema changes; removed ones leave the index, and calls to
// them fail on the server.
func (m *Manager) refresh(ctx context.Context, s *server, session *sdk.ClientSession) error {
	var listed []*sdk.Tool
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			return err
		}
		listed = append(listed, t)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old := s.tools
	s.tools = nil
	for _, st := range listed {
		name := toolName(s.name, st.Name)
		i := slices.IndexFunc(old, func(t *Tool) bool { return t.name == name })
		t := &Tool{name: name, server: s, vision: m.vision}
		if i >= 0 {
			t = old[i]
		}
		t.tool.Store(st)
		s.tools = append(s.tools, t)
	}
	slices.SortFunc(s.tools, func(a, b *Tool) int { return cmp.Compare(a.name, b.name) })
	return nil
}

// headerTransport adds a server's configured headers, typically its
// Authorization, to requests for its own host only. The client follows
// redirects through the transport too, and drops sensitive headers on a
// redirect to another host only when they were set on the request: added
// here unconditionally, a token would go wherever the server redirects.
type headerTransport struct {
	headers map[string]string
	host    string // the configured endpoint's host[:port]
}

func (h headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.EqualFold(req.URL.Host, h.host) {
		req = req.Clone(req.Context())
		for k, v := range h.headers {
			req.Header.Set(k, v)
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}

// Close shuts every server down.
func (m *Manager) Close() {
	for _, s := range m.servers {
		_ = s.session.Close()
	}
}

// Tools returns every server's current tools, in index order, named
// mcp__<server>__<tool>.
func (m *Manager) Tools() []*Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var tools []*Tool
	for _, s := range m.servers {
		tools = append(tools, s.tools...)
	}
	return tools
}

// Servers lists the connected servers' names in index order.
func (m *Manager) Servers() []string {
	names := make([]string, len(m.servers))
	for i, s := range m.servers {
		names[i] = s.name
	}
	return names
}

// invalidName matches what providers reject in a tool name.
var invalidName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// toolName builds a name OpenAI-compatible APIs accept: [a-zA-Z0-9_-],
// at most 64 characters.
func toolName(server, name string) string {
	n := invalidName.ReplaceAllString("mcp__"+server+"__"+name, "_")
	return n[:min(len(n), 64)]
}
