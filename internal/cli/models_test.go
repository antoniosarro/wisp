package cli

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
)

func TestChooseModel(t *testing.T) {
	chat := []model.Info{{ID: "a"}, {ID: "b", Loaded: true}, {ID: "embed", Embedding: true, Loaded: true}}
	cases := []struct {
		name      string
		models    []model.Info
		preferred []string
		want      string
	}{
		{"preferred first", chat, []string{"", "a"}, "a"},
		{"preferred but no longer served", chat, []string{"gone"}, "b"},
		{"the only loaded chat model", chat, nil, "b"},
		{"the only chat model", []model.Info{{ID: "a"}, {ID: "bge-embed", Embedding: true}}, nil, "a"},
		{"ambiguous", []model.Info{{ID: "a"}, {ID: "b"}}, nil, ""},
		{"nothing served", nil, nil, ""},
	}
	for _, c := range cases {
		if got := chooseModel(c.models, c.preferred...); got != c.want {
			t.Errorf("%s: chooseModel = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAmbiguousModelListsTheChoices(t *testing.T) {
	err := ambiguousModel([]model.Info{{ID: "a", ContextWindow: 8192}, {ID: "b"}})
	for _, want := range []string{"--model", "a  (8K context)", "b  (context unknown)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestParsePrice(t *testing.T) {
	p, err := parsePrice("3, 15,0.3")
	if err != nil || p != (model.Pricing{Known: true, Input: 3, Output: 15, CachedInput: 0.3}) {
		t.Fatalf("parsePrice = %+v, %v", p, err)
	}
	if p, err := parsePrice("0.27,1.10"); err != nil || p.CachedInput != 0 {
		t.Errorf("parsePrice without a cached rate = %+v, %v", p, err)
	}
	for _, bad := range []string{"3", "a,b", "1,-2", "1,2,3,4"} {
		if _, err := parsePrice(bad); err == nil {
			t.Errorf("parsePrice(%q) accepted", bad)
		}
	}
}

func TestOverrideAndApplyModel(t *testing.T) {
	price := model.Pricing{Known: true, Input: 1, Output: 2}
	cfg := Config{Vision: true, Price: price}
	info := cfg.override(model.Info{ID: "m", Vision: model.Unsupported, Tools: model.Unsupported, Price: model.Pricing{Known: true, Input: 9}})
	if info.Vision != model.Supported || info.Price != price {
		t.Fatalf("override = %+v, want the flags to win", info)
	}

	loop, vision := &core.Loop{}, new(atomic.Bool)
	applyModel(loop, vision, info)
	if !vision.Load() || !loop.NoTools || loop.Price != price {
		t.Errorf("applyModel: vision %v, NoTools %v, Price %+v", vision.Load(), loop.NoTools, loop.Price)
	}
}
