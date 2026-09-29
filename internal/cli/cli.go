// Package cli is the wisp command: flags, setup, and running a turn for
// the prompt given as arguments.
package cli

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/version"
)

// Main runs wisp and returns its exit code.
func Main() int {
	err := run()
	switch {
	case err == nil:
		return 0
	case errors.Is(err, context.Canceled):
		// Interrupted, as by Ctrl-C: 128+SIGINT, as shells report it, so
		// scripts and benchmarks can tell it from a failure.
		fmt.Fprintln(os.Stderr, "wisp: interrupted")
		return 130
	}
	fmt.Fprintln(os.Stderr, "wisp:", err)
	return 1
}

// run parses the flags and runs one turn for the prompt the arguments
// form, or the informational flags (--version, --models).
func run() error {
	var cfg Config
	flag.StringVar(&cfg.ModelName, "model", os.Getenv("WISP_MODEL"), "model to use (default: $WISP_MODEL, else the endpoint's only model)")
	flag.StringVar(&cfg.BaseURL, "base-url", cmp.Or(os.Getenv("WISP_BASE_URL"), "http://localhost:8000/v1"), "OpenAI-compatible API base URL (default: $WISP_BASE_URL)")
	// Not a flag default, which -h would print: the key would end up in
	// pasted help output. An explicit --api-key "" still sends no key.
	apiKeySet := false
	flag.Func("api-key", "API key (optional; default: $WISP_API_KEY)", func(v string) error {
		cfg.APIKey, apiKeySet = v, true
		return nil
	})
	flag.StringVar(&cfg.Provider, "provider", os.Getenv("WISP_PROVIDER"), "OpenRouter only: send every request to this upstream provider, e.g. deepinfra (default: $WISP_PROVIDER, else the cheapest zero-data-retention one)")
	flag.BoolVar(&cfg.Cheapest, "cheapest", false, "OpenRouter only: look up the model's two cheapest zero-data-retention providers and route only to them, cheapest first, in case account preferences override the price sort")
	flag.BoolVar(&cfg.SkipPermissions, "dangerously-skip-permissions", false, "skip permission prompts (dangerous)")
	flag.BoolVar(&cfg.Vision, "vision", false, "show image files to the model even when the endpoint doesn't report image input")
	flag.IntVar(&cfg.ContextWindow, "context-window", 0, "context window in tokens, overriding what the endpoint reports")
	flag.BoolVar(&cfg.NoSummarize, "no-summarize", false, "when the context fills, only mask old tool output; never summarize the conversation")
	flag.IntVar(&cfg.MaxIterations, "max-iterations", 0, "provider round-trips per turn before wrapping up (default 100)")
	flag.Func("price", "model price in US dollars per million tokens, as IN,OUT or IN,OUT,CACHED_IN, for cost estimates (default: $WISP_PRICE, else what the endpoint reports)", func(v string) (err error) {
		cfg.Price, err = parsePrice(v)
		return err
	})
	showVersion := flag.Bool("version", false, "print the version and exit")
	listModels := flag.Bool("models", false, "list the endpoint's models and what it reports about them, then exit")
	flag.Usage = func() {
		_, _ = fmt.Fprint(flag.CommandLine.Output(), "usage: wisp [flags] prompt...\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if !apiKeySet {
		cfg.APIKey = os.Getenv("WISP_API_KEY")
	}
	if env := os.Getenv("WISP_PRICE"); env != "" && !cfg.Price.Known {
		p, err := parsePrice(env)
		if err != nil {
			return fmt.Errorf("$WISP_PRICE: %w", err)
		}
		cfg.Price = p
	}

	if *showVersion {
		fmt.Println("wisp", version.Version)
		return nil
	}

	// SIGHUP and SIGTERM too: cancelling lets a running turn record its
	// tool results before exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	provider := newProvider(cfg)
	if *listModels {
		return printModels(ctx, provider, cfg.ModelName)
	}
	prompt := strings.Join(flag.Args(), " ")
	if prompt == "" {
		flag.Usage()
		return errors.New("no prompt given")
	}
	models, err := resolveModel(ctx, &cfg, provider)
	if err != nil {
		return err
	}
	if cfg.ModelName == "" {
		return ambiguousModel(models)
	}

	provider.SetModel(cfg.ModelName)
	info := cfg.override(describeModel(ctx, provider, cfg.ModelName))
	if info.Tools == model.Unsupported {
		fmt.Fprintf(os.Stderr, "wisp: the endpoint says %s cannot call tools; running it without tools\n", info.ID)
	}
	return runSingleShot(ctx, cfg, provider, info, prompt)
}
