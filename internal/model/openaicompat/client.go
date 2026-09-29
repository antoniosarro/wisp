// Package openaicompat implements model.Provider and model.Catalog for
// OpenAI-compatible chat completion APIs: local servers (llama.cpp,
// llama-swap, vLLM, SGLang, Ollama, LM Studio) and hosted ones such as
// OpenAI and OpenRouter.
//
// The package is laid out by concern:
//   - client.go: Config and the Client
//   - request.go: the outgoing chat request and its retries
//   - http.go: shared HTTP plumbing and error classification
//   - openrouter.go: OpenRouter's routing, attribution, and cost reporting
//   - stream.go, sse.go, think.go: reading the streamed response
//   - catalog.go, listing.go, native.go: model discovery
package openaicompat

import (
	"net/http"
	"sync"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

var (
	_ model.Provider = (*Client)(nil)
	_ model.Catalog  = (*Client)(nil)
)

// Config points a Client at a specific OpenAI-compatible endpoint.
type Config struct {
	BaseURL string // e.g. http://localhost:8000/v1
	Model   string
	APIKey  string // optional, sent as "Authorization: Bearer <key>"
	// Provider pins OpenRouter requests to one upstream provider (e.g.
	// "deepinfra") so its prompt cache stays warm. Ignored elsewhere.
	Provider string
	// Cheapest routes OpenRouter requests to the two cheapest
	// zero-data-retention providers of the model (see cheapestProviders).
	// Provider takes precedence. Ignored elsewhere.
	Cheapest bool
	// AppName and AppURL name the app to OpenRouter, which lists usage
	// under them (openrouter.ai/docs/app-attribution). Ignored elsewhere.
	AppName, AppURL string
}

// Client talks to one endpoint. It is safe for concurrent use: a Stream
// may run while the catalog is queried or the model switched.
type Client struct {
	cfg        Config
	http       *http.Client
	openRouter bool // cfg.BaseURL is OpenRouter's, which takes extra request fields

	mu sync.Mutex // guards cfg.Model and the discovery state below

	listing   listResponse    // latest /models response
	listedAt  time.Time       // when listing was fetched; zero before the first
	deadPaths map[string]bool // native endpoints this server doesn't have

	zdr      []zdrEndpoint       // OpenRouter's zero-data-retention endpoints, when Cheapest
	cheapest map[string][]string // model -> cheapestProviders' picks
}

// New returns a Client for cfg. A nil httpClient means
// http.DefaultClient; streams are bounded by their own idle timeout, not
// the client's.
func New(cfg Config, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		cfg:        cfg,
		http:       httpClient,
		openRouter: isOpenRouter(cfg.BaseURL),
		cheapest:   map[string][]string{},
	}
}

// SetModel switches the model for the next request. It is safe to call
// while a Stream is in flight; that stream keeps its model.
func (c *Client) SetModel(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Model = name
}

// Model returns the model requests currently use.
func (c *Client) Model() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Model
}
