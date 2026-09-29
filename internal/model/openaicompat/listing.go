package openaicompat

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
)

// listResponse is a /models response. The OpenAI spec gives each entry
// only an id; the other fields are what servers add of their own.
type listResponse struct {
	Data   []listEntry `json:"data"`
	Models []struct {
		Model        string   `json:"model"`
		Capabilities []string `json:"capabilities"`
	} `json:"models"` // llama.cpp's Ollama-style listing
}

// listEntry is one model of a /models listing, with every server's
// extensions; each field notes who sends it.
type listEntry struct {
	ID            string `json:"id"`
	OwnedBy       string `json:"owned_by"`
	MaxModelLen   int    `json:"max_model_len"`  // vLLM, SGLang
	ContextLength int    `json:"context_length"` // OpenRouter and others
	ContextWindow int    `json:"context_window"`
	Meta          *struct {
		NCtxTrain int `json:"n_ctx_train"`
		LlamaSwap *struct {
			ContextLength int `json:"context_length"`
		} `json:"llamaswap"` // llama-swap
	} `json:"meta"` // llama.cpp, llama-swap
	Architecture *struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"` // OpenRouter
	SupportedParameters []string `json:"supported_parameters"` // OpenRouter
	TopProvider         *struct {
		ContextLength       int `json:"context_length"`
		MaxCompletionTokens int `json:"max_completion_tokens"`
	} `json:"top_provider"` // OpenRouter
	Pricing *tokenPrices `json:"pricing"` // OpenRouter
}

// info converts the entry to what model.Info can say; fields the server
// left out stay unknown.
func (e listEntry) info() model.Info {
	info := model.Info{
		ID:            e.ID,
		ContextWindow: max(e.MaxModelLen, e.ContextLength, e.ContextWindow),
		Loaded:        e.OwnedBy == "llamacpp", // serves the one model it loaded
		Embedding:     looksLikeEmbedding(e.ID),
	}
	if m := e.Meta; m != nil {
		info.MaxContext = m.NCtxTrain
		if m.LlamaSwap != nil {
			info.ContextWindow = cmp.Or(info.ContextWindow, m.LlamaSwap.ContextLength)
		}
	}
	if p := e.TopProvider; p != nil {
		info.ContextWindow = cmp.Or(info.ContextWindow, p.ContextLength)
		info.MaxOutput = p.MaxCompletionTokens
	}
	if a := e.Architecture; a != nil {
		info.Vision = supportIf(slices.Contains(a.InputModalities, "image"))
		info.Embedding = info.Embedding || len(a.OutputModalities) > 0 && !slices.Contains(a.OutputModalities, "text")
	}
	if e.SupportedParameters != nil {
		info.Tools = supportIf(slices.Contains(e.SupportedParameters, "tools"))
		info.Reasoning = supportIf(slices.Contains(e.SupportedParameters, "reasoning"))
	}
	if p := e.Pricing; p != nil {
		info.Price = p.pricing()
	}
	return info
}

// tokenPrices is OpenRouter's price of a model or endpoint: US dollars per
// token, as strings.
type tokenPrices struct {
	Prompt         string `json:"prompt"`
	Completion     string `json:"completion"`
	InputCacheRead string `json:"input_cache_read"`
}

// pricing converts per-token prices to Pricing, which is per million.
// Unparsable or negative prices (OpenRouter's "-1" for routers whose price
// varies) leave the price unknown.
func (t tokenPrices) pricing() model.Pricing {
	in, errIn := strconv.ParseFloat(t.Prompt, 64)
	out, errOut := strconv.ParseFloat(t.Completion, 64)
	if errIn != nil || errOut != nil || in < 0 || out < 0 {
		return model.Pricing{}
	}
	p := model.Pricing{Known: true, Input: in * 1e6, Output: out * 1e6}
	if c, err := strconv.ParseFloat(t.InputCacheRead, 64); err == nil && c > 0 {
		p.CachedInput = c * 1e6
	}
	return p
}

// looksLikeEmbedding guesses from the id, for servers that list embedding
// and reranking models next to chat models without saying which is which.
func looksLikeEmbedding(id string) bool {
	id = strings.ToLower(id)
	return strings.Contains(id, "embed") || strings.Contains(id, "rerank")
}

// supportIf turns a capability the server did report into Supported or
// Unsupported; callers only use it when the server said either way.
func supportIf(ok bool) model.Support {
	if ok {
		return model.Supported
	}
	return model.Unsupported
}
