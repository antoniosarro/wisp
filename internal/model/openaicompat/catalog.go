package openaicompat

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

// Model discovery starts from the /models listing (listing.go) and, when
// that leaves context sizes out, fills in from local servers' native
// endpoints (native.go).

// listingTTL is how long a /models response is reused. It covers the
// listing and describing done together at startup and by /model; load
// state comes from the native probes, which always run fresh.
const listingTTL = 30 * time.Second

// Models lists the endpoint's models. When the listing lacks context
// sizes, local servers' native endpoints fill in load state and more
// (llama.cpp's /props only for a server with one model, which it describes).
func (c *Client) Models(ctx context.Context) ([]model.Info, error) {
	list, err := c.list(ctx)
	if err != nil {
		return nil, err
	}
	local := isLocalURL(c.cfg.BaseURL)
	infos := make([]model.Info, len(list.Data))
	for i, e := range list.Data {
		infos[i] = e.info()
		infos[i].Local = local
	}
	for _, m := range list.Models {
		if i := indexOf(infos, m.Model); i >= 0 && slices.Contains(m.Capabilities, "multimodal") {
			infos[i].Vision = model.Supported
		}
	}
	if slices.ContainsFunc(infos, func(i model.Info) bool { return i.ContextWindow == 0 }) {
		singleProps := func(ctx context.Context) []model.Info {
			if len(infos) != 1 {
				return nil
			}
			return c.llamaProps(ctx, infos[0].ID, true)
		}
		for _, extra := range c.probeAll(ctx, c.lmStudioModels, c.ollamaLoaded, singleProps) {
			if i := indexOf(infos, extra.ID); i >= 0 {
				merge(&infos[i], extra)
			}
		}
	}
	return infos, nil
}

// Describe returns what the listing says about id, completed by the
// native endpoints of llama.cpp and Ollama. It fails only when nothing
// could be learned at all.
func (c *Client) Describe(ctx context.Context, id string) (model.Info, error) {
	info := model.Info{ID: id, Local: isLocalURL(c.cfg.BaseURL)}
	infos, err := c.Models(ctx)
	if i := indexOf(infos, id); i >= 0 {
		info = infos[i]
	}
	if info.ContextWindow > 0 && info.Tools != model.SupportUnknown && info.Vision != model.SupportUnknown {
		return info, nil // nothing left for a probe to add
	}
	found := false
	for _, extra := range c.probeAll(ctx,
		func(ctx context.Context) []model.Info { return c.llamaProps(ctx, id, len(infos) == 1) },
		func(ctx context.Context) []model.Info { return c.ollamaShow(ctx, id) },
	) {
		merge(&info, extra)
		found = true
	}
	if err != nil && !found {
		return info, err
	}
	return info, nil
}

// list returns the /models response, reusing one younger than listingTTL.
func (c *Client) list(ctx context.Context) (listResponse, error) {
	c.mu.Lock()
	if time.Since(c.listedAt) < listingTTL {
		defer c.mu.Unlock()
		return c.listing, nil
	}
	c.mu.Unlock()
	var list listResponse
	if err := c.fetchJSON(ctx, http.MethodGet, c.cfg.BaseURL+"/models", nil, &list); err != nil {
		return list, fmt.Errorf("listing models: %w", err)
	}
	c.mu.Lock()
	c.listing, c.listedAt = list, time.Now()
	c.mu.Unlock()
	return list, nil
}

// merge fills what dst doesn't know from src; what dst knows wins.
func merge(dst *model.Info, src model.Info) {
	dst.ContextWindow = cmp.Or(dst.ContextWindow, src.ContextWindow)
	dst.MaxContext = cmp.Or(dst.MaxContext, src.MaxContext)
	dst.MaxOutput = cmp.Or(dst.MaxOutput, src.MaxOutput)
	dst.Tools = cmp.Or(dst.Tools, src.Tools)
	dst.Vision = cmp.Or(dst.Vision, src.Vision)
	dst.Reasoning = cmp.Or(dst.Reasoning, src.Reasoning)
	dst.Loaded = dst.Loaded || src.Loaded
	dst.Embedding = dst.Embedding || src.Embedding
	if !dst.Price.Known {
		dst.Price = src.Price
	}
}

// indexOf returns the position of the model id in infos, or -1.
func indexOf(infos []model.Info, id string) int {
	return slices.IndexFunc(infos, func(i model.Info) bool { return i.ID == id })
}

// isLocalURL reports whether rawURL points at this machine or a private
// network (loopback, private and link-local ranges, Tailscale's 100.64/10,
// single-label and .local/.lan/.home/.internal names), where a model
// server has no per-token bill.
func isLocalURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || tailnet.Contains(ip)
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".lan", ".home", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// tailnet is the carrier-grade NAT range Tailscale assigns addresses from.
var tailnet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
