package openaicompat

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/antoniosarro/wisp/internal/span"
)

// isOpenRouter reports whether rawURL is an OpenRouter endpoint.
func isOpenRouter(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && (u.Hostname() == "openrouter.ai" || strings.HasSuffix(u.Hostname(), ".openrouter.ai"))
}

// routing is OpenRouter's provider routing preferences. Requests go only
// to providers that neither train on nor retain prompts, the cheapest
// first. Price order is fixed, so a conversation keeps landing on the same
// provider and its prompt cache; pinning with Only makes that certain.
type routing struct {
	Only           []string `json:"only,omitempty"`
	Order          []string `json:"order,omitempty"`
	Sort           string   `json:"sort,omitempty"`
	DataCollection string   `json:"data_collection"`
	ZDR            bool     `json:"zdr"`
}

// usageRequest asks OpenRouter to report each request's billed cost.
type usageRequest struct {
	Include bool `json:"include"`
}

// route sets OpenRouter's routing and cost reporting on chatReq, and
// records the routing decision on the request's span:
//   - Config.Provider pins every request to that provider;
//   - Config.Cheapest sends them to our own two cheapest picks, in order;
//   - otherwise, and when the picks can't be looked up, OpenRouter's price
//     sort chooses.
func (c *Client) route(ctx context.Context, chatReq *chatRequest) {
	chatReq.Provider = &routing{DataCollection: "deny", ZDR: true, Sort: "price"}
	chatReq.Usage = &usageRequest{Include: true}
	sp := span.FromContext(ctx)
	switch {
	case c.cfg.Provider != "":
		chatReq.Provider.Only, chatReq.Provider.Sort = []string{c.cfg.Provider}, ""
		sp.Set("wisp.routing", "pinned")
		sp.Set("wisp.routing.order", []string{c.cfg.Provider})
	case c.cfg.Cheapest:
		// Our order, not OpenRouter's price sort, which was seen picking
		// the pricier of the two. A failing first falls back to the second.
		if picks := c.cheapestProviders(ctx, chatReq.Model); picks != nil {
			chatReq.Provider.Only, chatReq.Provider.Order, chatReq.Provider.Sort = picks, picks, ""
			sp.Set("wisp.routing", "cheapest")
			sp.Set("wisp.routing.order", picks)
			sp.Set("wisp.routing.rates", c.ratesOf(chatReq.Model, picks))
		} else {
			sp.Set("wisp.routing", "price sort (no cheapest providers found)")
		}
	default:
		sp.Set("wisp.routing", "price sort")
	}
}

// zdrEndpoint is one entry of OpenRouter's zero-data-retention endpoint list.
type zdrEndpoint struct {
	ModelID             string      `json:"model_id"`
	Tag                 string      `json:"tag"` // "<provider slug>[/<variant>]"
	Pricing             tokenPrices `json:"pricing"`
	SupportedParameters []string    `json:"supported_parameters"`
}

// slug is the provider part of the endpoint's tag.
func (e zdrEndpoint) slug() string {
	slug, _, _ := strings.Cut(e.Tag, "/")
	return slug
}

// weightedCost ranks endpoints by the cost of 10 prompt tokens per
// completion token: an agent's requests are mostly prompt, though caching
// makes it cheaper than its token count suggests.
func weightedCost(input, output float64) float64 { return 10*input + output }

// cheapestProviders returns the slugs of the two cheapest distinct
// providers serving id with zero data retention and tool calling, cheapest
// first, or nil when OpenRouter can't say. OpenRouter's own price sort
// can't be relied on: account preferences may rank a pricier provider
// first. Picks are cached per model; the endpoint list is fetched once.
func (c *Client) cheapestProviders(ctx context.Context, id string) []string {
	c.mu.Lock()
	picks, ok := c.cheapest[id]
	endpoints := c.zdr
	c.mu.Unlock()
	if ok {
		return picks
	}
	if endpoints == nil {
		var resp struct {
			Data []zdrEndpoint `json:"data"`
		}
		if err := c.fetchJSON(ctx, http.MethodGet, c.cfg.BaseURL+"/endpoints/zdr", nil, &resp); err != nil {
			return nil // not cached: the next request retries
		}
		endpoints = resp.Data
	}
	picks = pickCheapest(endpoints, id)
	c.mu.Lock()
	c.zdr, c.cheapest[id] = endpoints, picks
	c.mu.Unlock()
	return picks
}

// pickCheapest ranks id's tool-calling endpoints with a known price by
// weightedCost and returns the first two distinct providers.
func pickCheapest(endpoints []zdrEndpoint, id string) []string {
	type priced struct {
		slug string
		cost float64
	}
	var candidates []priced
	for _, e := range endpoints {
		if e.ModelID != id || !slices.Contains(e.SupportedParameters, "tools") {
			continue
		}
		if p := e.Pricing.pricing(); p.Known {
			candidates = append(candidates, priced{e.slug(), weightedCost(p.Input, p.Output)})
		}
	}
	// Stable, so equal prices keep OpenRouter's order and picks don't flap.
	slices.SortStableFunc(candidates, func(a, b priced) int { return cmp.Compare(a.cost, b.cost) })
	var picks []string
	for _, p := range candidates {
		if len(picks) < 2 && !slices.Contains(picks, p.slug) {
			picks = append(picks, p.slug)
		}
	}
	return picks
}

// providerRates is a routed provider's price per 1M tokens, for the trace.
type providerRates struct {
	Provider  string  `json:"provider"`
	Input     float64 `json:"input_per_m"`
	Output    float64 `json:"output_per_m"`
	CacheRead float64 `json:"cache_read_per_m,omitempty"`
}

// ratesOf returns each of picks' cheapest rates for id, in picks' order.
// A provider can list several endpoints (variants) for one model.
func (c *Client) ratesOf(id string, picks []string) []providerRates {
	c.mu.Lock()
	endpoints := c.zdr
	c.mu.Unlock()
	out := make([]providerRates, 0, len(picks))
	for _, slug := range picks {
		r := providerRates{Provider: slug}
		best := -1.0
		for _, e := range endpoints {
			if e.ModelID != id || e.slug() != slug {
				continue
			}
			p := e.Pricing.pricing()
			if cost := weightedCost(p.Input, p.Output); p.Known && (best < 0 || cost < best) {
				best, r.Input, r.Output, r.CacheRead = cost, p.Input, p.Output, p.CachedInput
			}
		}
		out = append(out, r)
	}
	return out
}
