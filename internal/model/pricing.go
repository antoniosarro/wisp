package model

import (
	"fmt"
	"strconv"
	"strings"
)

// Pricing is what a model's tokens cost, in US dollars per million.
type Pricing struct {
	Known       bool // the endpoint gave prices; zero rates then mean free
	Input       float64
	Output      float64
	CachedInput float64 // 0 means cached input is billed like other input
}

// Cost prices one request's usage: prompt tokens (cached of them served
// from the provider's cache) and completion tokens. cached is clamped to
// [0, prompt], so a provider's inconsistent report can't make it negative.
func (p Pricing) Cost(prompt, cached, completion int) float64 {
	cachedRate := p.CachedInput
	if cachedRate == 0 {
		cachedRate = p.Input
	}
	cached = min(max(cached, 0), prompt)
	return (float64(prompt-cached)*p.Input + float64(cached)*cachedRate + float64(completion)*p.Output) / 1e6
}

// String is the price as "$3.00 in · $15.00 out per 1M", with a cached rate
// when it differs.
func (p Pricing) String() string {
	s := fmt.Sprintf("$%s in · $%s out", rate(p.Input), rate(p.Output))
	if p.CachedInput > 0 && p.CachedInput != p.Input {
		s += fmt.Sprintf(" · $%s cached", rate(p.CachedInput))
	}
	return s + " per 1M"
}

// rate formats a per-million price with at least two decimals, like
// money, and more when the price has them: cheap models and fractional
// rates ($0.625) must not be rounded into a different price. Ten
// significant digits drop the noise of converting per-token prices
// (1.1400000000000001), which no real price has.
func rate(v float64) string {
	v, _ = strconv.ParseFloat(strconv.FormatFloat(v, 'g', 10, 64), 64)
	s := strconv.FormatFloat(v, 'f', -1, 64)
	dot := strings.IndexByte(s, '.')
	switch {
	case dot < 0:
		return s + ".00"
	case len(s)-dot == 2:
		return s + "0"
	}
	return s
}
