package model

import (
	"context"
	"fmt"
	"strings"
)

// Support is whether a model has a capability, as far as its endpoint says.
type Support int8

const (
	SupportUnknown Support = iota // the endpoint didn't say
	Supported
	Unsupported
)

// Info describes one model as its endpoint reports it; zero values mean
// the endpoint didn't say.
type Info struct {
	ID            string
	ContextWindow int // tokens the server accepts per request
	MaxContext    int // the model's trained maximum, which the server may not allow
	MaxOutput     int // output-token cap per response
	Tools         Support
	Vision        Support
	Reasoning     Support
	Efforts       Efforts // reasoning effort levels it accepts
	Loaded        bool    // in memory now (local servers)
	Embedding     bool    // not a chat model
	Local         bool    // served on this machine or network: no per-token bill
	Price         Pricing // what its tokens cost, when the endpoint says
}

// Summary describes what is known about the model in one line, such as
// "32K context · tools · vision". Capabilities the endpoint didn't report
// are left out rather than guessed.
func (i Info) Summary() string {
	parts := []string{i.context()}
	switch i.Tools {
	case Supported:
		parts = append(parts, "tools")
	case Unsupported:
		parts = append(parts, "no tools") // worth saying: the agent can't act without them
	}
	for _, flag := range []struct {
		on   bool
		name string
	}{
		{i.Vision == Supported, "vision"},
		{i.Reasoning == Supported, "reasoning"},
		{i.Embedding, "embedding"},
		{i.Loaded, "loaded"},
	} {
		if flag.on {
			parts = append(parts, flag.name)
		}
	}
	if levels := i.Efforts.Levels(); len(levels) > 0 {
		parts = append(parts, "effort "+strings.Join(levels, "/"))
	}
	if i.Price.Known {
		parts = append(parts, fmt.Sprintf("$%s/$%s per 1M", rate(i.Price.Input), rate(i.Price.Output)))
	}
	return strings.Join(parts, " · ")
}

// context describes the context window, and the model's own maximum when
// the server allows less.
func (i Info) context() string {
	switch {
	case i.ContextWindow > 0 && i.MaxContext > i.ContextWindow:
		return fmt.Sprintf("%s context (model max %s)", tokens(i.ContextWindow), tokens(i.MaxContext))
	case i.ContextWindow > 0:
		return tokens(i.ContextWindow) + " context"
	case i.MaxContext > 0:
		return fmt.Sprintf("context unknown (model max %s)", tokens(i.MaxContext))
	}
	return "context unknown"
}

// tokens abbreviates a token count. Powers of two read as binary units,
// since that is how most windows are sized: 32768 -> "32K", 1048576 ->
// "1M"; other counts round to decimal ones: 200000 -> "200K".
func tokens(n int) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%dM", n>>20)
	case n >= 1_000_000:
		return fmt.Sprintf("%.3gM", float64(n)/1e6)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%dK", n>>10)
	case n >= 1000:
		return fmt.Sprintf("%.0fK", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// Catalog is implemented by providers that serve several models: it lists
// them, describes one in detail, and switches between them.
type Catalog interface {
	// Models lists what the endpoint serves, with what the listing reports.
	Models(ctx context.Context) ([]Info, error)
	// Describe gathers everything the endpoint reports about one model.
	Describe(ctx context.Context, id string) (Info, error)
	// Model is the model requests use; SetModel changes it for the next one.
	Model() string
	SetModel(id string)
}
