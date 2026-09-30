package tui

import (
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

func TestCostAccumulatesPerRequestPrice(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.opts.Model = model.Info{ID: "pricey", Price: model.Pricing{Known: true, Input: 3, Output: 15, CachedInput: 0.3}}
	usage := core.StepStats{UsageAvailable: true, PromptTokens: 100_000, CachedTokens: 50_000, CompletionTokens: 10_000}
	m.Update(StatsMsg(usage)) // 0.15 + 0.015 + 0.15 = 0.315

	m.opts.Model = model.Info{ID: "cheap", Price: model.Pricing{Known: true, Input: 0.1, Output: 0.2}}
	m.Update(StatsMsg(usage)) // 0.01 + 0.002 = 0.012

	m.opts.Model = model.Info{ID: "hosted, no price"}
	m.Update(StatsMsg(usage))

	c := m.cost
	if c.Session < 0.3269 || c.Session > 0.3271 || c.Unpriced != 1 || c.LastPriced {
		t.Fatalf("cost = %+v, want session 0.327 with one unpriced request", c)
	}
	out := stripANSI(renderDebugPanel(m.stats, m.opts, c, 0, sidePanelWidth))
	for _, want := range []string{"Cost (est.)", "spent       —         $0.327", "1 request(s) had no known price"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel missing %q:\n%s", want, out)
		}
	}
}

func TestCostExplainsLocalAndUnknown(t *testing.T) {
	render := func(info model.Info) string {
		return stripANSI(renderDebugPanel(core.StepStats{}, Options{Model: info}, costSummary{}, 0, sidePanelWidth))
	}
	if out := render(model.Info{ID: "q", Local: true}); !strings.Contains(out, "local endpoint: no API cost") {
		t.Errorf("local:\n%s", out)
	}
	if out := render(model.Info{ID: "q"}); !strings.Contains(out, "--price IN,OUT sets it ($/1M)") {
		t.Errorf("unknown:\n%s", out)
	}
	if out := render(model.Info{ID: "q", Price: model.Pricing{Known: true, Input: 0.27, Output: 1.1}}); !strings.Contains(out, "$0.27 in · $1.10 out per 1M") {
		t.Errorf("price:\n%s", out)
	}
}

func TestCostPrefersBilledCost(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	// A list price that must be ignored once the endpoint bills.
	m.opts.Model = model.Info{ID: "or", Price: model.Pricing{Known: true, Input: 3, Output: 15}}
	usage := core.StepStats{UsageAvailable: true, PromptTokens: 100_000, CompletionTokens: 10_000}

	billed := usage
	billed.Cost, billed.CostReported, billed.Provider = 0.02, true, "DeepInfra"
	m.Update(StatsMsg(billed))
	billed.Cost, billed.Provider = 0.05, "Together" // the provider changed
	m.Update(StatsMsg(billed))

	c := m.cost
	if c.Session < 0.0699 || c.Session > 0.0701 || c.Last != 0.05 || c.Billed != 2 {
		t.Fatalf("cost = %+v, want the billed 0.02 + 0.05", c)
	}
	out := stripANSI(renderDebugPanel(m.stats, m.opts, c, 0, sidePanelWidth))
	for _, want := range []string{"via Together", "Cost (billed)", "spent       $0.050    $0.070"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "per 1M") {
		t.Errorf("billed panel shows the unused list price:\n%s", out)
	}

	m.Update(StatsMsg(usage)) // an unbilled request falls back to list price: 0.3 + 0.15
	if c := m.cost; c.Session < 0.5199 || c.Session > 0.5201 || c.Estimated != 1 {
		t.Fatalf("after unbilled request cost = %+v, want 0.52 with one estimate", c)
	}
}
