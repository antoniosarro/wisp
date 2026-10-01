package openaicompat

import (
	"context"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func TestModelsMarksEmbeddingModels(t *testing.T) {
	c := fakeServer(t, map[string]string{"GET /v1/models": `{"data":[
		{"id":"chat","max_model_len":1},
		{"id":"bge-reranker","max_model_len":1},
		{"id":"img","max_model_len":1,"architecture":{"output_modalities":["embeddings"]}}]}`})
	got, err := c.Models(context.Background())
	if err != nil || len(got) != 3 || got[0].Embedding || !got[1].Embedding || !got[2].Embedding {
		t.Fatalf("Models = %+v, %v", got, err)
	}
}

func TestTokenPrices(t *testing.T) {
	for _, c := range []struct {
		name  string
		in    tokenPrices
		known bool
	}{
		{"per-token prices", tokenPrices{Prompt: "0.000001", Completion: "0.000002"}, true},
		{"free", tokenPrices{Prompt: "0", Completion: "0"}, true},
		{"router's variable price", tokenPrices{Prompt: "-1", Completion: "-1"}, false},
		{"missing", tokenPrices{}, false},
	} {
		if p := c.in.pricing(); p.Known != c.known {
			t.Errorf("%s: parsed as %+v, want known %v", c.name, p, c.known)
		}
	}
	if p := (tokenPrices{Prompt: "0.000001", Completion: "0.000002", InputCacheRead: "0.0000001"}).pricing(); round6(p.Input) != 1 || round6(p.Output) != 2 || round6(p.CachedInput) != 0.1 {
		t.Errorf("per-million = %+v, want 1, 2, and 0.1 cached", p)
	}
}

func TestOpenRouterEfforts(t *testing.T) {
	infos, err := fakeServer(t, map[string]string{"GET /v1/models": `{"data":[
		{"id":"must-think","supported_parameters":["reasoning"],"reasoning":{"mandatory":true,"supported_efforts":["high","low"]}},
		{"id":"may-think","supported_parameters":["reasoning"],"reasoning":{"mandatory":false,"default_enabled":true}},
		{"id":"unsaid","supported_parameters":["reasoning"]},
		{"id":"no-think","supported_parameters":["tools"],"reasoning":{"mandatory":false}},
		{"id":"plain"}]}`}).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]model.Efforts{
		"must-think": model.EffortsReported | model.EffortsOf("low", "high"),
		"may-think":  model.EffortsReported | model.EffortsOf("none"),
		"unsaid":     0,
		"no-think":   model.EffortsReported,
		"plain":      0,
	}
	for _, i := range infos {
		if i.Efforts != want[i.ID] {
			t.Errorf("%s: efforts %v (%v), want %v", i.ID, i.Efforts, i.Efforts.Levels(), want[i.ID])
		}
	}
}
