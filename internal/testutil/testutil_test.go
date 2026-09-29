package testutil

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func TestScriptedProviderReplaysTurnsInOrder(t *testing.T) {
	p := &ScriptedProvider{Turns: [][]model.Event{
		{{Kind: model.EventTextDelta, Text: "first"}, {Kind: model.EventDone}},
		{{Kind: model.EventTextDelta, Text: "second"}, {Kind: model.EventDone}},
	}}
	for _, want := range []string{"first", "second"} {
		req := model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: want}}}
		events, err := p.Stream(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		var got []model.Event
		for e := range events {
			got = append(got, e)
		}
		if len(got) != 2 || got[0].Text != want || p.LastReq.Messages[0].Content != want {
			t.Errorf("turn %q: events %+v, last request %+v", want, got, p.LastReq)
		}
	}
	if _, err := p.Stream(context.Background(), model.Request{}); err == nil || p.Calls != 2 {
		t.Errorf("past the script: err %v, calls %d; want an error and 2 calls", err, p.Calls)
	}
}

func TestEchoTool(t *testing.T) {
	res, err := EchoTool{}.Run(context.Background(), json.RawMessage(`{"a":1}`))
	if err != nil || res.Content != `echoed: {"a":1}` || (EchoTool{}).Risky() {
		t.Errorf("Run = %+v, %v", res, err)
	}
}
