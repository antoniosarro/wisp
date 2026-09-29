package cli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/termsafe"
)

// discoveryTimeout bounds the model listing and description at startup.
const discoveryTimeout = 5 * time.Second

// resolveModel sets cfg.ModelName when it wasn't given: to the resumed
// session's model, else the last one used with this endpoint, else the
// endpoint's only loaded or only chat model. It returns the listing, so
// the caller can name the choices when several models remain.
func resolveModel(ctx context.Context, cfg *Config, catalog model.Catalog) ([]model.Info, error) {
	if cfg.ModelName != "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	models, err := catalog.Models(ctx)
	if err != nil {
		return nil, fmt.Errorf("no --model given, and listing the endpoint's models failed: %w", err)
	}
	cfg.ModelName = chooseModel(models, resumedModel(cfg.ResumeID), loadState().LastModel[cfg.BaseURL])
	return models, nil
}

// chooseModel returns the first preferred id the endpoint serves, else its
// only loaded chat model, else its only chat model, else "".
func chooseModel(models []model.Info, preferred ...string) string {
	for _, id := range preferred {
		if id != "" && slices.ContainsFunc(models, func(m model.Info) bool { return m.ID == id }) {
			return id
		}
	}
	var chat, loaded []string
	for _, m := range models {
		if m.Embedding {
			continue
		}
		chat = append(chat, m.ID)
		if m.Loaded {
			loaded = append(loaded, m.ID)
		}
	}
	switch {
	case len(loaded) == 1:
		return loaded[0]
	case len(chat) == 1:
		return chat[0]
	}
	return ""
}

// ambiguousModel is the error for a run without a way to pick a model; it
// lists the choices.
func ambiguousModel(models []model.Info) error {
	var b strings.Builder
	b.WriteString("the endpoint serves several models; choose one with --model or $WISP_MODEL:")
	for _, m := range models {
		fmt.Fprintf(&b, "\n  %s  (%s)", termsafe.Strip(m.ID), m.Summary())
	}
	return fmt.Errorf("%s", b.String())
}

// describeModel asks the endpoint about id. When it can't answer, the
// model runs with nothing known about it.
func describeModel(ctx context.Context, catalog model.Catalog, id string) model.Info {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	info, err := catalog.Describe(ctx, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wisp: could not read details of %s: %v\n", id, err)
	}
	info.ID = id
	return info
}

// override applies the flags that correct what the endpoint reports; they
// hold for every model used in the run.
func (cfg Config) override(info model.Info) model.Info {
	if cfg.ContextWindow > 0 {
		info.ContextWindow = cfg.ContextWindow
	}
	if cfg.Vision {
		info.Vision = model.Supported
	}
	if cfg.Price.Known {
		info.Price = cfg.Price
	}
	return info
}

// parsePrice reads "IN,OUT" or "IN,OUT,CACHED_IN" in dollars per million
// tokens, e.g. "0.27,1.10" or "3,15,0.30".
func parsePrice(s string) (model.Pricing, error) {
	fields := strings.Split(s, ",")
	if len(fields) < 2 || len(fields) > 3 {
		return model.Pricing{}, fmt.Errorf("price %q: want IN,OUT or IN,OUT,CACHED_IN in dollars per million tokens", s)
	}
	rates := make([]float64, 3)
	for i, f := range fields {
		v, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
		if err != nil || v < 0 {
			return model.Pricing{}, fmt.Errorf("price %q: %q is not a non-negative number", s, f)
		}
		rates[i] = v
	}
	return model.Pricing{Known: true, Input: rates[0], Output: rates[1], CachedInput: rates[2]}, nil
}

// applyModel configures the loop and the read tool for a model.
func applyModel(loop *core.Loop, vision *atomic.Bool, info model.Info) {
	loop.ContextWindow, loop.MaxOutput = info.ContextWindow, info.MaxOutput
	loop.Price = info.Price
	loop.NoTools = info.Tools == model.Unsupported
	loop.Presummarize = info.Local // idle time on a local server is free
	vision.Store(info.Vision == model.Supported)
}

// printModels lists the endpoint's models for --models, marking current.
func printModels(ctx context.Context, catalog model.Catalog, current string) error {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	models, err := catalog.Models(ctx)
	if err != nil {
		return err
	}
	for _, m := range models {
		marker := " "
		if m.ID == current {
			marker = "*"
		}
		// Model ids come from the endpoint: they may not drive the terminal.
		fmt.Printf("%s %s  (%s)\n", marker, termsafe.Strip(m.ID), m.Summary())
	}
	return nil
}
