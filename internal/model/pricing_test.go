package model

import (
	"math"
	"testing"
)

func TestPricingCost(t *testing.T) {
	cached := Pricing{Known: true, Input: 3, Output: 15, CachedInput: 0.3}
	flat := Pricing{Known: true, Input: 1, Output: 2}
	for _, c := range []struct {
		name                       string
		p                          Pricing
		prompt, cached, completion int
		want                       float64
	}{
		// 600K uncached at $3, 400K cached at $0.30, 100K out at $15.
		{"cached rate", cached, 1_000_000, 400_000, 100_000, 1.8 + 0.12 + 1.5},
		{"no cached rate bills cache as input", flat, 1_000_000, 500_000, 0, 1},
		{"cached above prompt is clamped", cached, 1_000_000, 2_000_000, 0, 0.3},
		{"negative cached is clamped", cached, 1_000_000, -5, 0, 3},
		{"nothing used", cached, 0, 0, 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.Cost(c.prompt, c.cached, c.completion); math.Abs(got-c.want) > 1e-9 {
				t.Errorf("Cost(%d, %d, %d) = %v, want %v", c.prompt, c.cached, c.completion, got, c.want)
			}
		})
	}
}

func TestPricingString(t *testing.T) {
	for _, c := range []struct {
		p    Pricing
		want string
	}{
		{Pricing{Input: 3, Output: 15, CachedInput: 0.3}, "$3.00 in · $15.00 out · $0.30 cached per 1M"},
		{Pricing{Input: 3, Output: 15, CachedInput: 3}, "$3.00 in · $15.00 out per 1M"},
		{Pricing{Input: 1.25, Output: 10}, "$1.25 in · $10.00 out per 1M"},
		// Fractional and tiny rates keep every digit instead of rounding.
		{Pricing{Input: 0.625, Output: 0.05}, "$0.625 in · $0.05 out per 1M"},
		{Pricing{Input: 0.0375, Output: 0.15}, "$0.0375 in · $0.15 out per 1M"},
	} {
		if got := c.p.String(); got != c.want {
			t.Errorf("%+v.String() = %q, want %q", c.p, got, c.want)
		}
	}
}
