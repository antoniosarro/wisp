package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/antoniosarro/wisp/internal/model"
)

// Local servers answer native endpoints, under the server root, with what
// /models leaves out: llama.cpp /props, Ollama /api/show and /api/ps, and
// LM Studio /api/v0/models. Every server 404s the others' endpoints, so a
// probe that fails is skipped, and one that 404s is not asked again.

// errNoEndpoint is what probing a path the server lacks returns.
var errNoEndpoint = errors.New("endpoint not available on this server")

// rootURL is the server root under the OpenAI-compatible base, where
// native endpoints live: http://host:8080/v1 -> http://host:8080.
func (c *Client) rootURL() string {
	return strings.TrimSuffix(strings.TrimRight(c.cfg.BaseURL, "/"), "/v1")
}

// probe calls a native endpoint under the server root, unless the server
// already said it has no such endpoint. Only a definite "not here" (404,
// 405, 501) to a GET rules a path out: a POST names a model, and Ollama
// answers 404 for an unknown one. Timeouts and other errors are retried.
func (c *Client) probe(ctx context.Context, method, path string, body, out any) error {
	if c.dead(path) {
		return errNoEndpoint
	}
	err := c.fetchJSON(ctx, method, c.rootURL()+path, body, out)
	var se *statusError
	if method == http.MethodGet && errors.As(err, &se) && (se.code == http.StatusNotFound || se.code == http.StatusMethodNotAllowed || se.code == http.StatusNotImplemented) {
		c.mu.Lock()
		if c.deadPaths == nil {
			c.deadPaths = map[string]bool{}
		}
		c.deadPaths[path] = true
		c.mu.Unlock()
	}
	return err
}

// dead reports whether the server said it has no endpoint at path.
func (c *Client) dead(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadPaths[path]
}

// probeAll runs probes concurrently and collects what they found.
func (c *Client) probeAll(ctx context.Context, probes ...func(context.Context) []model.Info) []model.Info {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var all []model.Info
	for _, probe := range probes {
		wg.Go(func() {
			found := probe(ctx)
			mu.Lock()
			all = append(all, found...)
			mu.Unlock()
		})
	}
	wg.Wait()
	return all
}

// llamaProps reads llama.cpp's /props: the per-slot context the server
// runs with, and its modalities. It describes the loaded model, so it only
// counts for id when that is the one served (only) or matches the model
// file.
func (c *Client) llamaProps(ctx context.Context, id string, only bool) []model.Info {
	var p struct {
		ModelPath string `json:"model_path"`
		Settings  struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
		Modalities *struct {
			Vision bool `json:"vision"`
		} `json:"modalities"`
		TemplateCaps *struct {
			SupportsToolCalls *bool `json:"supports_tool_calls"`
		} `json:"chat_template_caps"`
	}
	if c.probe(ctx, http.MethodGet, "/props", nil, &p) != nil || p.Settings.NCtx == 0 {
		return nil
	}
	if !only && !strings.Contains(p.ModelPath, id) {
		return nil
	}
	info := model.Info{ID: id, ContextWindow: p.Settings.NCtx, Loaded: true}
	if p.Modalities != nil {
		info.Vision = supportIf(p.Modalities.Vision)
	}
	if t := p.TemplateCaps; t != nil && t.SupportsToolCalls != nil {
		info.Tools = supportIf(*t.SupportsToolCalls)
	}
	return []model.Info{info}
}

// numCtxParam finds num_ctx in Ollama's Modelfile parameters, one per line.
var numCtxParam = regexp.MustCompile(`(?m)^num_ctx\s+(\d+)`)

// ollamaShow reads Ollama's /api/show: capabilities, the trained context,
// and a num_ctx the model sets for itself. The trained context is under
// "<architecture>.context_length"; other keys with that suffix, such as a
// vision encoder's, are not the model's.
func (c *Client) ollamaShow(ctx context.Context, id string) []model.Info {
	if c.dead("/api/ps") { // not Ollama
		return nil
	}
	var s struct {
		Parameters   string                     `json:"parameters"`
		ModelInfo    map[string]json.RawMessage `json:"model_info"`
		Capabilities []string                   `json:"capabilities"`
	}
	if c.probe(ctx, http.MethodPost, "/api/show", map[string]string{"model": id}, &s) != nil || s.ModelInfo == nil {
		return nil
	}
	info := model.Info{ID: id}
	var arch string
	if json.Unmarshal(s.ModelInfo["general.architecture"], &arch) == nil {
		info.MaxContext, _ = strconv.Atoi(string(s.ModelInfo[arch+".context_length"]))
	}
	if m := numCtxParam.FindStringSubmatch(s.Parameters); m != nil {
		info.ContextWindow, _ = strconv.Atoi(m[1])
	}
	if caps := s.Capabilities; len(caps) > 0 {
		info.Tools = supportIf(slices.Contains(caps, "tools"))
		info.Vision = supportIf(slices.Contains(caps, "vision"))
		info.Reasoning = supportIf(slices.Contains(caps, "thinking"))
		info.Embedding = slices.Contains(caps, "embedding") && !slices.Contains(caps, "completion")
	}
	return []model.Info{info}
}

// ollamaLoaded reads Ollama's /api/ps: which models are loaded, and the
// context each was loaded with, which is what requests get.
func (c *Client) ollamaLoaded(ctx context.Context) []model.Info {
	var ps struct {
		Models []struct {
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
		} `json:"models"`
	}
	if c.probe(ctx, http.MethodGet, "/api/ps", nil, &ps) != nil {
		return nil
	}
	infos := make([]model.Info, len(ps.Models))
	for i, m := range ps.Models {
		infos[i] = model.Info{ID: m.Name, ContextWindow: m.ContextLength, Loaded: true}
	}
	return infos
}

// lmStudioModels reads LM Studio's /api/v0/models: model type (vlm means
// vision), load state, and context sizes. Models without native tool use
// still get LM Studio's generic tool prompt, so tools are never ruled out.
func (c *Client) lmStudioModels(ctx context.Context) []model.Info {
	var list struct {
		Data []struct {
			ID                  string   `json:"id"`
			Type                string   `json:"type"`
			State               string   `json:"state"`
			MaxContextLength    int      `json:"max_context_length"`
			LoadedContextLength int      `json:"loaded_context_length"`
			Capabilities        []string `json:"capabilities"`
		} `json:"data"`
	}
	if c.probe(ctx, http.MethodGet, "/api/v0/models", nil, &list) != nil {
		return nil
	}
	infos := make([]model.Info, 0, len(list.Data))
	for _, m := range list.Data {
		info := model.Info{
			ID:            m.ID,
			ContextWindow: m.LoadedContextLength,
			MaxContext:    m.MaxContextLength,
			Loaded:        m.State == "loaded",
			Embedding:     m.Type == "embeddings",
		}
		switch m.Type {
		case "vlm":
			info.Vision = model.Supported
		case "llm":
			info.Vision = model.Unsupported
		}
		if slices.Contains(m.Capabilities, "tool_use") {
			info.Tools = model.Supported
		}
		infos = append(infos, info)
	}
	return infos
}
