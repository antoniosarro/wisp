// Package mcp connects to MCP servers and exposes their tools through two
// fixed tools, so the tool list, and with it the prompt cache, stays the
// same all session:
//   - config.go: reading mcp.json
//   - client.go: connecting servers and listing their tools (Manager)
//   - tool.go: one server tool, its risk, and converting its results
//   - search.go: tool_search, which returns tools' definitions as a result
//   - call.go: mcp_call, which validates and runs a call behind the tool's
//     own permission gate
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
)

// ServerConfig is one entry of mcp.json's mcpServers: Command for a stdio
// server, URL for a Streamable HTTP one.
type ServerConfig struct {
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Disabled bool              `json:"disabled"`

	// Direct puts the server's tools in the tool list, as built-in ones
	// are, instead of behind tool_search and mcp_call: for a server with a
	// few tools the model uses all the time. They are listed at startup.
	Direct bool `json:"direct"`
	// ReadOnly declares the server's tools read-only, so they run without
	// asking: for a trusted server that sends no annotations, like gopls.
	ReadOnly bool `json:"readOnly"`
	// Instructions, when set, replaces what the server sends at startup.
	Instructions *string `json:"instructions"`
}

// LoadConfig reads mcp.json files over builtin (Builtin), later ones
// replacing earlier entries of the same name; missing files are skipped.
// Disabled servers are dropped and environment variables expanded.
func LoadConfig(builtin map[string]ServerConfig, paths ...string) (map[string]ServerConfig, error) {
	servers := maps.Clone(builtin)
	if servers == nil {
		servers = map[string]ServerConfig{}
	}
	for _, path := range paths {
		file, err := ReadConfig(path)
		if err != nil {
			return nil, err
		}
		maps.Copy(servers, file)
	}
	for name, s := range servers {
		if s.Disabled {
			delete(servers, name)
			continue
		}
		if (s.Command == "") == (s.URL == "") {
			return nil, fmt.Errorf("mcp server %q: set exactly one of command and url", name)
		}
		servers[name] = s.expanded()
	}
	return servers, nil
}

// ReadConfig returns one mcp.json's servers as written: disabled ones
// included, variables not expanded. A missing file has none.
func ReadConfig(path string) (map[string]ServerConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct {
		MCPServers map[string]ServerConfig `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file.MCPServers, nil
}

// expanded is s with ${VAR} expanded from the environment, so secrets stay
// out of the file.
func (s ServerConfig) expanded() ServerConfig {
	s.URL = os.ExpandEnv(s.URL)
	s.Args = expandAll(s.Args)
	s.Env = expandValues(s.Env)
	s.Headers = expandValues(s.Headers)
	return s
}

// expandAll expands every string of in.
func expandAll(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = os.ExpandEnv(v)
	}
	return out
}

// expandValues expands every value of in.
func expandValues(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = os.ExpandEnv(v)
	}
	return out
}
