package builtin

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/textfmt"
	"github.com/antoniosarro/wisp/internal/tool"
	"github.com/antoniosarro/wisp/internal/version"
)

// BraveSearchURL is Brave Search's API: an endpoint set to it uses Brave,
// any other is a SearXNG instance.
const BraveSearchURL = "https://api.search.brave.com/res/v1/web/search"

const (
	defaultSearchResults = 5
	maxSearchResults     = 10
	maxSnippetBytes      = 300     // of each result's text
	maxSearchDownload    = 2 << 20 // a results page is far smaller
	searchTimeout        = 30 * time.Second
)

// WebSearchTool searches the web through the endpoint the user set:
// SearXNG, or Brave Search when Endpoint is BraveSearchURL. The endpoint
// is the user's choice, not the model's, so it isn't held to fetch's
// address checks: a SearXNG on localhost is the usual setup.
type WebSearchTool struct {
	Endpoint string       // SearXNG's base URL, or BraveSearchURL
	Key      string       // Brave's subscription token; SearXNG takes none
	Client   *http.Client // nil: a client with searchTimeout
}

func (WebSearchTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name:        "web_search",
		Description: "Search the web. Returns titles, URLs and short snippets; read a result with fetch.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "what to search for"},
				"count": {"type": "integer", "description": "number of results (default 5, at most 10)"}
			},
			"required": ["query"]
		}`),
	}
}

// Risky: the query leaves the machine, and the model writes it.
func (WebSearchTool) Risky() bool { return true }

// searchResult is one hit, from either provider.
type searchResult struct{ title, url, snippet string }

func (t WebSearchTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return tool.Result{}, errors.New("query is empty")
	}
	n := min(cmp.Or(max(a.Count, 0), defaultSearchResults), maxSearchResults)

	var results []searchResult
	if t.Endpoint == BraveSearchURL {
		results, err = t.brave(ctx, query, n)
	} else {
		results, err = t.searxng(ctx, query, n)
	}
	if err != nil {
		return tool.Result{}, err
	}
	if len(results) == 0 {
		return tool.Result{Content: fmt.Sprintf("No results for %q.", query)}, nil
	}
	var b strings.Builder
	for i, r := range results[:min(n, len(results))] {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, cleanSnippet(r.title), r.url)
		if s := cleanSnippet(r.snippet); s != "" {
			fmt.Fprintf(&b, "   %s\n", s)
		}
		b.WriteString("\n")
	}
	return tool.Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}

// searxng asks a SearXNG instance for JSON results. Its base URL alone
// means its /search page.
func (t WebSearchTool) searxng(ctx context.Context, query string, n int) ([]searchResult, error) {
	u, err := url.Parse(t.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("search endpoint %q: %w", t.Endpoint, err)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/search"
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()
	var page struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	status, err := t.get(ctx, u.String(), nil, &page)
	if status == http.StatusForbidden {
		return nil, errors.New("SearXNG refused to answer in JSON: add json to search.formats in its settings.yml")
	}
	if err != nil {
		return nil, err
	}
	var out []searchResult
	for _, r := range page.Results[:min(n, len(page.Results))] {
		out = append(out, searchResult{r.Title, r.URL, r.Content})
	}
	return out, nil
}

// brave asks Brave Search's API, with the key.
func (t WebSearchTool) brave(ctx context.Context, query string, n int) ([]searchResult, error) {
	q := url.Values{"q": {query}, "count": {fmt.Sprint(n)}}
	var page struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	status, err := t.get(ctx, BraveSearchURL+"?"+q.Encode(), http.Header{"X-Subscription-Token": {t.Key}}, &page)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, errors.New("the Brave Search key in $WISP_SEARCH_KEY was refused")
	}
	if err != nil {
		return nil, err
	}
	var out []searchResult
	for _, r := range page.Web.Results {
		out = append(out, searchResult{r.Title, r.URL, r.Description})
	}
	return out, nil
}

// get fetches a JSON reply into v, returning the HTTP status as well, so
// callers can explain the refusals they know.
func (t WebSearchTool) get(ctx context.Context, u string, header http.Header, v any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	client := cmp.Or(t.Client, &http.Client{Timeout: searchTimeout})
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("searching: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := io.LimitReader(resp.Body, maxSearchDownload)
	if resp.StatusCode != http.StatusOK {
		brief, _ := io.ReadAll(io.LimitReader(body, 500))
		return resp.StatusCode, fmt.Errorf("search endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(brief)))
	}
	if err := json.NewDecoder(body).Decode(v); err != nil {
		return resp.StatusCode, fmt.Errorf("reading search results: %w", err)
	}
	return resp.StatusCode, nil
}

// htmlTag matches the markup some providers put in snippets (Brave's
// <strong> around matched words).
var htmlTag = regexp.MustCompile(`<[^>]*>`)

// cleanSnippet is s as plain text on one line, at most maxSnippetBytes.
func cleanSnippet(s string) string {
	s = strings.Join(strings.Fields(html.UnescapeString(htmlTag.ReplaceAllString(s, ""))), " ")
	if len(s) > maxSnippetBytes {
		s = textfmt.Prefix(s, maxSnippetBytes) + "…"
	}
	return s
}
