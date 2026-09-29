package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/span"
)

const openRouterURL = "http://openrouter.ai/api/v1"

func TestOpenRouterRouting(t *testing.T) {
	srv, got := chatServer(t)
	httpClient := redirectClient(srv)
	for _, c := range []struct{ name, baseURL, pin, want string }{
		{"price sort", openRouterURL, "", `"provider":{"sort":"price","data_collection":"deny","zdr":true},"usage":{"include":true}`},
		{"pinned", openRouterURL, "deepinfra", `"provider":{"only":["deepinfra"],"data_collection":"deny","zdr":true}`},
		{"not OpenRouter", "http://localhost:8091/v1", "deepinfra", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := postBody(t, New(Config{BaseURL: c.baseURL, Model: "m", Provider: c.pin}, httpClient), got, model.Request{})
			if c.want == "" && strings.Contains(body, `"provider"`) {
				t.Errorf("body = %s, want no provider preferences", body)
			}
			if c.want != "" && !strings.Contains(body, c.want) {
				t.Errorf("body = %s, want %s", body, c.want)
			}
		})
	}
}

// zdrServer serves OpenRouter's endpoint list from zdr (or a 500 when it
// is empty) and answers chat requests with an empty stream.
func zdrServer(t *testing.T, zdr string) (*Client, *captured, *atomic.Int32) {
	t.Helper()
	var listings atomic.Int32
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/endpoints/zdr" {
			listings.Add(1)
			if zdr == "" {
				http.Error(w, "unavailable", http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, zdr)
			return
		}
		b, _ := io.ReadAll(r.Body)
		got.mu.Lock()
		got.body = string(b)
		got.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: openRouterURL, Model: "m", Cheapest: true}, redirectClient(srv)), got, &listings
}

func TestCheapestRoutesToTwoCheapestProviders(t *testing.T) {
	ep := func(modelID, tag, prompt, completion string, tools bool) map[string]any {
		params := []string{"temperature"}
		if tools {
			params = append(params, "tools")
		}
		return map[string]any{"model_id": modelID, "tag": tag, "pricing": map[string]string{"prompt": prompt, "completion": completion}, "supported_parameters": params}
	}
	zdr, _ := json.Marshal(map[string]any{"data": []any{
		ep("m", "together", "0.00000015", "0.0000005", true),
		ep("m", "sail-research/fp8", "0.000000045", "0.0000006", true),
		ep("m", "deepinfra/fp4", "0.000000075", "0.00000025", true),
		ep("m", "notools", "0.00000001", "0.00000001", false), // cheapest, but can't call tools
		ep("other", "cheapo", "0", "0", true),                 // another model
		ep("m", "sail-research/us", "0.000000045", "0.0000006", true),
		ep("m", "inference-net/fp4", "0.000000045", "0.00000014", true),
	}})
	c, got, listings := zdrServer(t, string(zdr))

	sink := &spanSink{}
	rec := span.NewRecorder(sink, "s")
	ctx, sp := rec.Start(context.Background(), span.KindRequest, "m")
	want := `"provider":{"only":["inference-net","deepinfra"],"order":["inference-net","deepinfra"],"data_collection":"deny","zdr":true}`
	for range 2 {
		if body := postBodyCtx(t, ctx, c, got, model.Request{}); !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	if n := listings.Load(); n != 1 {
		t.Errorf("endpoint list fetched %d times, want once", n)
	}

	// The trace gets the routing: mode, order, and each pick's rates per 1M.
	sp.End(span.StatusOK)
	rec.Close()
	a := sink.last.Attrs
	rates, _ := json.Marshal(a["wisp.routing.rates"])
	if a["wisp.routing"] != "cheapest" || !reflect.DeepEqual(a["wisp.routing.order"], []string{"inference-net", "deepinfra"}) ||
		!strings.Contains(string(rates), `{"provider":"inference-net","input_per_m":0.045,"output_per_m":0.14}`) {
		t.Errorf("routing attrs = %v, rates %s", a, rates)
	}
}

func TestCheapestFallsBackToPriceSort(t *testing.T) {
	c, got, listings := zdrServer(t, "")
	for range 2 {
		if body := postBody(t, c, got, model.Request{}); !strings.Contains(body, `"sort":"price"`) || strings.Contains(body, `"only"`) {
			t.Fatalf("body = %s, want OpenRouter's price sort", body)
		}
	}
	if n := listings.Load(); n != 2 {
		t.Errorf("endpoint list fetched %d times, want a retry per request after a failure", n)
	}
}
