package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// searchRun runs web_search with args, failing on a Go error.
func searchRun(t *testing.T, s WebSearchTool, args string) string {
	t.Helper()
	res, err := s.Run(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return res.Content
}

func TestWebSearchSearXNG(t *testing.T) {
	var asked url.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = *r.URL
		var results []string
		for i := range 15 {
			results = append(results, fmt.Sprintf(`{"title":"Result %d","url":"https://example.com/%d","content":"text &amp; more\n%s"}`, i, i, strings.Repeat("x", 400)))
		}
		_, _ = fmt.Fprintf(w, `{"results":[%s]}`, strings.Join(results, ","))
	}))
	defer srv.Close()

	got := searchRun(t, WebSearchTool{Endpoint: srv.URL}, `{"query":"go generics","count":3}`)
	if asked.Path != "/search" || asked.Query().Get("q") != "go generics" || asked.Query().Get("format") != "json" {
		t.Errorf("asked %s, want /search?q=go+generics&format=json", asked.String())
	}
	if strings.Count(got, "https://example.com/") != 3 || !strings.Contains(got, "1. Result 0\n   https://example.com/0\n   text & more x") {
		t.Errorf("results:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if len(line) > maxSnippetBytes+10 {
			t.Errorf("snippet not clipped: %d bytes", len(line))
		}
	}
	if got := searchRun(t, WebSearchTool{Endpoint: srv.URL}, `{"query":"q","count":50}`); strings.Count(got, "https://") != maxSearchResults {
		t.Errorf("count 50 gave %d results, want at most %d", strings.Count(got, "https://"), maxSearchResults)
	}
}

func TestWebSearchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("q") {
		case "empty":
			_, _ = fmt.Fprint(w, `{"results":[]}`)
		default: // SearXNG without json in search.formats
			http.Error(w, "Forbidden", http.StatusForbidden)
		}
	}))
	defer srv.Close()
	s := WebSearchTool{Endpoint: srv.URL + "/"}
	if got := searchRun(t, s, `{"query":"empty"}`); got != `No results for "empty".` {
		t.Errorf("no results: %q", got)
	}
	if _, err := s.Run(context.Background(), json.RawMessage(`{"query":"x"}`)); err == nil || !strings.Contains(err.Error(), "search.formats") {
		t.Errorf("403 from SearXNG: err = %v, want the settings.yml hint", err)
	}
	if _, err := s.Run(context.Background(), json.RawMessage(`{"query":"  "}`)); err == nil {
		t.Error("an empty query ran")
	}
}

// toServer sends every request to srv, whatever its URL: Brave's API has
// a fixed address.
type toServer struct{ srv *httptest.Server }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.srv.URL)
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestWebSearchBrave(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/res/v1/web/search" || r.URL.Query().Get("count") != "2" {
			t.Errorf("asked %s", r.URL)
		}
		_, _ = fmt.Fprint(w, `{"web":{"results":[{"title":"Go","url":"https://go.dev","description":"The <strong>Go</strong> language"}]}}`)
	}))
	defer srv.Close()
	client := &http.Client{Transport: toServer{srv}}

	got := searchRun(t, WebSearchTool{Endpoint: BraveSearchURL, Key: "key", Client: client}, `{"query":"golang","count":2}`)
	if got != "1. Go\n   https://go.dev\n   The Go language" {
		t.Errorf("results: %q", got)
	}
	_, err := WebSearchTool{Endpoint: BraveSearchURL, Key: "wrong", Client: client}.Run(context.Background(), json.RawMessage(`{"query":"golang"}`))
	if err == nil || !strings.Contains(err.Error(), "$WISP_SEARCH_KEY") {
		t.Errorf("a wrong key: err = %v", err)
	}
}
