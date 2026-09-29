package model

import "testing"

func TestInfoSummary(t *testing.T) {
	for _, c := range []struct {
		name string
		info Info
		want string
	}{
		{"nothing reported", Info{}, "context unknown"},
		{
			"window below model max",
			Info{ContextWindow: 8192, MaxContext: 40960, Tools: Supported, Reasoning: Supported, Loaded: true},
			"8K context (model max 40K) · tools · reasoning · loaded",
		},
		{"only model max", Info{MaxContext: 131072, Tools: Unsupported}, "context unknown (model max 128K) · no tools"},
		{"window at model max", Info{ContextWindow: 32768, MaxContext: 32768}, "32K context"},
		{"vision", Info{ContextWindow: 200000, Vision: Supported}, "200K context · vision"},
		{"embedding", Info{ContextWindow: 512, Embedding: true}, "512 context · embedding"},
		{"unknown and unsupported extras hidden", Info{ContextWindow: 4096, Vision: Unsupported, Reasoning: SupportUnknown}, "4K context"},
		{"price", Info{ContextWindow: 1024, Price: Pricing{Known: true, Input: 0.05, Output: 0.08}}, "1K context · $0.05/$0.08 per 1M"},
		{"free price", Info{ContextWindow: 1024, Price: Pricing{Known: true}}, "1K context · $0.00/$0.00 per 1M"},
		{"unknown price hidden", Info{ContextWindow: 1024, Price: Pricing{Input: 3}}, "1K context"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.info.Summary(); got != c.want {
				t.Errorf("Summary() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	for n, want := range map[int]string{
		0:         "0",
		512:       "512",
		1000:      "1K",
		1024:      "1K",
		32768:     "32K",
		163840:    "160K",
		200000:    "200K",
		1_000_000: "1M",
		1_048_576: "1M",
		2_097_152: "2M",
		1_500_000: "1.5M",
	} {
		if got := tokens(n); got != want {
			t.Errorf("tokens(%d) = %q, want %q", n, got, want)
		}
	}
}
