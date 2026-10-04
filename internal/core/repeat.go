package core

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// repeatLimit is how often one sentence may recur in a response's
// reasoning before it counts as a loop. Models stuck that way announce an
// action again and again ("Let me run the tests now.") without making the
// call, until the output limit. On recorded responses, 8 caught 10 of 13
// that ran into the limit, and stopped none that ended on their own except
// other such loops.
const repeatLimit = 8

// minRepeatLen is the shortest sentence counted: fillers like "Let me
// check." recur in sound reasoning too.
const minRepeatLen = 20

// codeLine matches a line of code. Reasoning quotes the same line again as
// it works through it, so code doesn't count as repetition.
var codeLine = regexp.MustCompile(`[{};]|:=|==|!=|&&|\|\||<-|\w\(|^(return|case|func|if|for|go|defer|var|const|type|package|import)\b|^//|^_ =`)

// repeatWatch finds a sentence that recurs repeatLimit times in a response's
// reasoning, fed to it as it streams in. The answer isn't watched: it may
// repeat a line on purpose (a refrain, a line asked for ten times).
type repeatWatch struct {
	pending string // text after the last complete sentence
	counts  map[string]int
}

// add takes the next streamed text and returns the sentence that reached
// repeatLimit with it, or "". A sentence ends at a line break, or at . ? !
// or : followed by a space; its spacing is normalized before counting.
func (w *repeatWatch) add(s string) string {
	w.pending += s
	start := 0
	for i := 0; i < len(w.pending); i++ {
		end := -1
		switch w.pending[i] {
		case '\n':
			end = i
		case '.', '?', '!', ':':
			if i+1 < len(w.pending) && (w.pending[i+1] == ' ' || w.pending[i+1] == '\t') {
				end = i + 1
			}
		}
		if end < 0 {
			continue
		}
		if repeated := w.count(w.pending[start:end]); repeated != "" {
			return repeated
		}
		start = end
	}
	w.pending = w.pending[start:]
	return ""
}

// count adds one occurrence of sentence and reports it once it reaches
// repeatLimit.
func (w *repeatWatch) count(sentence string) string {
	sentence = strings.Join(strings.Fields(sentence), " ")
	if len(sentence) < minRepeatLen || codeLine.MatchString(sentence) {
		return ""
	}
	if w.counts == nil {
		w.counts = map[string]int{}
	}
	w.counts[sentence]++
	if w.counts[sentence] < repeatLimit {
		return ""
	}
	return sentence
}

// repeatReminder follows a response stopped for repeating sentence.
func repeatReminder(sentence string) string {
	if len(sentence) > 120 {
		cut := 120
		for !utf8.RuneStart(sentence[cut]) {
			cut--
		}
		sentence = sentence[:cut] + "…"
	}
	return fmt.Sprintf("Your previous response was stopped: it said %q %d times without acting, and any tool calls in it were dropped. Do not repeat your reasoning. Act now: make the next tool call, or give a short final answer.", sentence, repeatLimit)
}
