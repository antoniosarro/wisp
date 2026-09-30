package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/core"
	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/testutil"
)

func TestCompactionBlock(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.handleTurnMsg(CompactMsg{})
	if got := transcriptText(m); !strings.Contains(got, "Compacting context…") {
		t.Fatalf("render() = %q, want the running compaction", got)
	}
	m.handleTurnMsg(CompactMsg{Done: true, Compaction: core.Compaction{TokensBefore: 12_000, TokensAfter: 3_400}})
	got := transcriptText(m)
	if !strings.Contains(got, "Compacted context: 12.0K → 3.4K tokens") || strings.Contains(got, "Compacting context") {
		t.Errorf("render() = %q, want the finished compaction in place of the running one", got)
	}

	m.handleTurnMsg(CompactMsg{})
	m.finishTurn(context.Canceled)
	if got := transcriptText(m); !strings.Contains(got, "Compaction stopped") || strings.Contains(got, "Compacting context") {
		t.Errorf("render() = %q, want the cancelled compaction settled", got)
	}
}

func TestCompactCommandWithNothingToCompact(t *testing.T) {
	m, ch := newTestModel(t, &testutil.ScriptedProvider{})
	m.input.SetValue("/compact")
	m.submit()
	if !m.inTurn {
		t.Fatal("/compact didn't start")
	}
	for msg := range ch {
		if done, ok := msg.(TurnDoneMsg); ok {
			m.Update(done)
			break
		}
	}
	if got := transcriptText(m); m.inTurn || !strings.Contains(got, "nothing to compact") {
		t.Errorf("inTurn %v, render() = %q", m.inTurn, got)
	}
}

func TestReplayMarksCompaction(t *testing.T) {
	m, _ := newTestModel(t, &testutil.ScriptedProvider{})
	m.loop.History = []model.Message{
		{Role: model.RoleUser, Content: "old question"},
		{Role: model.RoleAssistant, Content: "old answer"},
		{Role: model.RoleUser, Content: "new question"},
		{Role: model.RoleAssistant, Content: "new answer"},
	}
	m.loop.Compactions = []core.Compaction{{FirstKept: 1, TokensBefore: 3000, TokensAfter: 800}, {FirstKept: 2, TokensBefore: 5000, TokensAfter: 900}}
	m.loop.Compacted = &m.loop.Compactions[1]
	m.blocks = nil
	m.replayHistory(m.loop.History)
	got := transcriptText(m)
	first, answer, second, fresh := strings.Index(got, "3.0K → 800"), strings.Index(got, "old answer"), strings.Index(got, "5.0K → 900"), strings.Index(got, "new question")
	if first < 0 || answer < first || second < answer || fresh < second {
		t.Errorf("render() = %q, want each compaction marked where it began the model's view", got)
	}
}
