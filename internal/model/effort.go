package model

// effortLevels are the reasoning effort levels wisp knows, weakest first:
// the names OpenAI's reasoning_effort and OpenRouter's reasoning.effort
// use. "none" turns reasoning off.
var effortLevels = [...]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// Efforts is the set of reasoning effort levels a model accepts, one bit
// per level of effortLevels, plus EffortsReported. Zero means the endpoint
// didn't say.
type Efforts uint8

// EffortsReported marks a set the endpoint reported, so that one with no
// levels means the model takes none rather than that nobody said.
const EffortsReported Efforts = 1 << len(effortLevels)

// EffortsOf is the set of the given levels; names wisp doesn't know are
// left out.
func EffortsOf(levels ...string) Efforts {
	var e Efforts
	for _, l := range levels {
		for i, known := range effortLevels {
			if l == known {
				e |= 1 << i
			}
		}
	}
	return e
}

// AllEfforts is every level wisp knows, for a model whose levels the
// endpoint didn't report.
var AllEfforts = EffortsOf(effortLevels[:]...)

// Has reports whether level is in the set.
func (e Efforts) Has(level string) bool {
	l := EffortsOf(level)
	return l != 0 && e&l == l
}

// Levels lists the set's levels, weakest first.
func (e Efforts) Levels() []string {
	var levels []string
	for i, l := range effortLevels {
		if e&(1<<i) != 0 {
			levels = append(levels, l)
		}
	}
	return levels
}
