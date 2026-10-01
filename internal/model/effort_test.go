package model

import (
	"slices"
	"testing"
)

func TestEfforts(t *testing.T) {
	e := EffortsOf("max", "low", "ultra", "none")
	if got, want := e.Levels(), []string{"none", "low", "max"}; !slices.Equal(got, want) {
		t.Errorf("Levels = %v, want %v (weakest first, unknown names dropped)", got, want)
	}
	for level, want := range map[string]bool{"low": true, "none": true, "medium": false, "ultra": false, "": false} {
		if got := e.Has(level); got != want {
			t.Errorf("Has(%q) = %v, want %v", level, got, want)
		}
	}
	if EffortsReported.Levels() != nil || EffortsReported.Has("none") {
		t.Error("the reported mark reads as a level")
	}
	if Efforts(0).Levels() != nil || !AllEfforts.Has("xhigh") {
		t.Error("the empty set has levels, or AllEfforts misses one")
	}
}
