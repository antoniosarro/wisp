package cli

import (
	"slices"
	"sync/atomic"
	"testing"
)

// web_search exists only with an endpoint: otherwise its schema would
// cost prompt tokens on every request, for a call that can only fail.
func TestWebSearchOnlyWithAnEndpoint(t *testing.T) {
	names := func(cfg Config) []string {
		var out []string
		for _, tl := range builtinTools(cfg, &atomic.Bool{}) {
			out = append(out, tl.Schema().Name)
		}
		return out
	}
	if got := names(Config{}); slices.Contains(got, "web_search") {
		t.Errorf("tools without an endpoint: %v", got)
	}
	if got := names(Config{SearchURL: "http://localhost:8888"}); !slices.Contains(got, "web_search") {
		t.Errorf("tools with an endpoint: %v", got)
	}
}
