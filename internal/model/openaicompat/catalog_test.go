package openaicompat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func TestDescribeByServer(t *testing.T) {
	cases := []struct {
		name   string
		routes map[string]string
		id     string
		want   model.Info
	}{
		{
			name:   "vLLM",
			routes: map[string]string{"GET /v1/models": `{"data":[{"id":"qwen","owned_by":"vllm","max_model_len":32768}]}`},
			id:     "qwen",
			want:   model.Info{ID: "qwen", ContextWindow: 32768},
		},
		{
			name: "llama.cpp",
			routes: map[string]string{
				"GET /v1/models": `{"models":[{"model":"q.gguf","capabilities":["completion","multimodal"]}],"data":[{"id":"q.gguf","owned_by":"llamacpp","meta":{"n_ctx_train":131072}}]}`,
				"GET /props":     `{"model_path":"/m/q.gguf","default_generation_settings":{"n_ctx":16384},"chat_template_caps":{"supports_tool_calls":true}}`,
			},
			id:   "q.gguf",
			want: model.Info{ID: "q.gguf", ContextWindow: 16384, MaxContext: 131072, Tools: model.Supported, Vision: model.Supported, Loaded: true},
		},
		{
			name: "llama-swap",
			routes: map[string]string{
				"GET /v1/models": `{"data":[{"id":"coder-35b","owned_by":"llama-swap","meta":{"llamaswap":{"context_length":131072}}}]}`,
			},
			id:   "coder-35b",
			want: model.Info{ID: "coder-35b", ContextWindow: 131072},
		},
		{
			name: "Ollama",
			routes: map[string]string{
				"GET /v1/models": `{"data":[{"id":"qwen3:8b","owned_by":"library"},{"id":"nomic-embed-text","owned_by":"library"}]}`,
				"GET /api/ps":    `{"models":[{"name":"qwen3:8b","context_length":8192}]}`,
				// The architecture's key is the model's; a vision encoder's is not.
				"POST /api/show": `{"parameters":"num_ctx 4096","model_info":{"general.architecture":"qwen3","qwen3.context_length":40960,"qwen3.vision.context_length":1024},"capabilities":["completion","tools","thinking"]}`,
			},
			id:   "qwen3:8b",
			want: model.Info{ID: "qwen3:8b", ContextWindow: 8192, MaxContext: 40960, Tools: model.Supported, Vision: model.Unsupported, Reasoning: model.Supported, Loaded: true},
		},
		{
			name: "LM Studio",
			routes: map[string]string{
				"GET /v1/models":     `{"data":[{"id":"qwen2-vl"}]}`,
				"GET /api/v0/models": `{"data":[{"id":"qwen2-vl","type":"vlm","state":"loaded","max_context_length":32768,"loaded_context_length":8192,"capabilities":["tool_use"]}]}`,
			},
			id:   "qwen2-vl",
			want: model.Info{ID: "qwen2-vl", ContextWindow: 8192, MaxContext: 32768, Tools: model.Supported, Vision: model.Supported, Loaded: true},
		},
		{
			name: "OpenRouter",
			routes: map[string]string{"GET /v1/models": `{"data":[{"id":"deepseek/v3","context_length":163840,
				"architecture":{"input_modalities":["text"],"output_modalities":["text"]},
				"top_provider":{"context_length":163840,"max_completion_tokens":65536},
				"supported_parameters":["tools","reasoning","max_tokens"],
				"pricing":{"prompt":"0.00000027","completion":"0.0000011","input_cache_read":"0.00000007"}}]}`},
			id: "deepseek/v3",
			want: model.Info{ID: "deepseek/v3", ContextWindow: 163840, MaxOutput: 65536, Tools: model.Supported, Vision: model.Unsupported, Reasoning: model.Supported,
				Price: model.Pricing{Known: true, Input: 0.27, Output: 1.1, CachedInput: 0.07}},
		},
		{
			name:   "plain OpenAI",
			routes: map[string]string{"GET /v1/models": `{"data":[{"id":"gpt-x","owned_by":"openai"}]}`},
			id:     "gpt-x",
			want:   model.Info{ID: "gpt-x"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := fakeServer(t, c.routes).Describe(context.Background(), c.id)
			c.want.Local = true // every fake server listens on 127.0.0.1
			if p := got.Price; p.Known {
				got.Price = model.Pricing{Known: true, Input: round6(p.Input), Output: round6(p.Output), CachedInput: round6(p.CachedInput)}
			}
			if err != nil || got != c.want {
				t.Fatalf("Describe = %+v, %v\nwant       %+v", got, err, c.want)
			}
		})
	}
}

func TestDescribeFailsOnlyWhenNothingAnswers(t *testing.T) {
	if _, err := fakeServer(t, nil).Describe(context.Background(), "x"); err == nil {
		t.Fatal("Describe succeeded against an endpoint that answered nothing")
	}
}

func TestDiscoveryAvoidsRepeatRequests(t *testing.T) {
	var mu sync.Mutex // probes run concurrently
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"a"},{"id":"b"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL + "/v1"}, nil)
	ctx := context.Background()

	_, _ = c.Models(ctx)
	_, _ = c.Describe(ctx, "a")
	mu.Lock()
	first := len(hits)
	mu.Unlock()
	// The listing is cached and every native endpoint 404'd: no requests.
	_, _ = c.Models(ctx)
	_, _ = c.Describe(ctx, "b")
	mu.Lock()
	defer mu.Unlock()
	if first > 5 || len(hits) != first {
		t.Fatalf("requests: %d for the first list+describe, %d more for the second; want at most 5, then none: %v", first, len(hits)-first, hits)
	}
}

// Ollama 404s /api/show for an unknown model, which must not rule the
// endpoint out for the next one.
func TestOllamaUnknownModelKeepsShowProbe(t *testing.T) {
	var mu sync.Mutex
	shows := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"qwen3:8b"}]}`)
		case "/api/ps":
			_, _ = io.WriteString(w, `{"models":[]}`)
		case "/api/show":
			mu.Lock()
			shows++
			first := shows == 1
			mu.Unlock()
			if first {
				http.Error(w, `{"error":"model 'typo' not found"}`, http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"model_info":{"general.architecture":"qwen3","qwen3.context_length":40960},"capabilities":["completion","tools"]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL + "/v1"}, nil)
	_, _ = c.Describe(context.Background(), "typo")
	got, err := c.Describe(context.Background(), "qwen3:8b")
	if err != nil || got.Tools != model.Supported || got.MaxContext != 40960 {
		t.Fatalf("Describe after an unknown model = %+v, %v", got, err)
	}
}

func TestIsLocalURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://localhost:8000/v1":     true,
		"http://127.0.0.1:11434/v1":    true,
		"http://192.168.1.20:8080/v1":  true,
		"http://100.101.1.2:8000/v1":   true, // Tailscale
		"http://gpu-box:8000/v1":       true,
		"http://llm.lan/v1":            true,
		"https://openrouter.ai/api/v1": false,
		"https://api.deepseek.com/v1":  false,
		"http://[::1]:8000/v1":         true,
		"https://100.200.0.1/v1":       false, // outside 100.64/10
	} {
		if got := isLocalURL(raw); got != want {
			t.Errorf("isLocalURL(%q) = %v, want %v", raw, got, want)
		}
	}
}
