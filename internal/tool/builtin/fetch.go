package builtin

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/textfmt"
	"github.com/antoniosarro/wisp/internal/tool"
	"github.com/antoniosarro/wisp/internal/version"
)

const (
	defaultFetchBytes = 20_000   // text one call returns when it sets no max_bytes
	maxFetchBytes     = 100_000  // largest part one call may return
	maxFetchDownload  = 5 << 20  // bytes downloaded before the rest is dropped
	maxErrorDownload  = 64 << 10 // an error page is only shown in brief
	maxErrorText      = 2000     // bytes of an error page's text shown
	fetchTimeout      = 30 * time.Second
)

// FetchTool retrieves web pages as text. Client defaults to one with a
// timeout that follows redirects only within the host (sameHostRedirect).
type FetchTool struct {
	Client *http.Client
}

func (FetchTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name:        "fetch",
		Description: "Fetch an http(s) URL and return its content as text; HTML pages are converted to readable text. Long pages are returned in parts: pass offset to continue. Downloads stop at 5 MB.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"url": {"type": "string", "description": "http or https URL"},
				"max_bytes": {"type": "integer", "description": "maximum bytes of text to return (default 20000, at most 100000)"},
				"offset": {"type": "integer", "description": "byte offset into the text to continue a truncated fetch"}
			},
			"required": ["url"]
		}`),
	}
}

func (FetchTool) Risky() bool { return true }

// sameHostRedirect follows redirects within the host the call was approved
// for. One to another host is returned instead, so following it is a new
// call, and asks: an allowed host could otherwise redirect to a local
// service or a cloud metadata address.
func sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
		return http.ErrUseLastResponse
	}
	return nil
}

func (t FetchTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[struct {
		URL      string `json:"url"`
		MaxBytes int    `json:"max_bytes"`
		Offset   int    `json:"offset"`
	}](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return tool.Result{}, fmt.Errorf("url must be an absolute http or https URL")
	}

	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout, CheckRedirect: sameHostRedirect}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return tool.Result{}, err
	}
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return tool.Result{}, fmt.Errorf("fetching %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if loc := resp.Header.Get("Location"); resp.StatusCode >= 300 && resp.StatusCode < 400 && loc != "" {
		if next, err := resp.Request.URL.Parse(loc); err == nil {
			loc = next.String()
		}
		return tool.Result{Content: fmt.Sprintf("%s redirects to %s, on another host; fetch that URL to follow it", resp.Request.URL, loc)}, nil
	}
	body := &io.LimitedReader{R: resp.Body, N: maxFetchDownload}
	if resp.StatusCode >= 400 {
		body.N = maxErrorDownload
	}
	br := bufio.NewReader(body)

	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		// No usable Content-Type: judge by the first bytes instead.
		head, _ := br.Peek(512)
		mediaType, _, _ = mime.ParseMediaType(http.DetectContentType(head))
	}
	var text string
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		text, err = htmlToText(br)
	case strings.HasPrefix(mediaType, "text/"), strings.HasSuffix(mediaType, "json"), strings.HasSuffix(mediaType, "xml"):
		var raw []byte
		raw, err = io.ReadAll(br)
		text = string(raw)
	default:
		return tool.Result{Content: fmt.Sprintf("%s returned %s, which is not text", u, mediaType), IsError: true}, nil
	}
	if err != nil {
		return tool.Result{}, fmt.Errorf("reading %s: %w", u, err)
	}

	text = strings.TrimSpace(text)
	if resp.StatusCode >= 400 {
		return tool.Result{Content: fmt.Sprintf("%s returned %s\n\n%s", u, resp.Status, truncateText(text, maxErrorText)), IsError: true}, nil
	}
	limit := min(cmp.Or(max(a.MaxBytes, 0), defaultFetchBytes), maxFetchBytes)
	content := page(text, a.Offset, limit)
	if body.N == 0 {
		content += fmt.Sprintf("\n... (download stopped at %d MB; the rest of the page is not available)", maxFetchDownload>>20)
	}
	return tool.Result{Content: content}, nil
}

// page returns up to limit bytes of text from byte offset on, cut on rune
// boundaries, with a note on how to fetch the next part.
func page(text string, offset, limit int) string {
	if offset > 0 && offset >= len(text) {
		return fmt.Sprintf("(offset %d is past the end of the %d-byte text)", offset, len(text))
	}
	offset = max(0, offset)
	for offset > 0 && !utf8.RuneStart(text[offset]) {
		offset--
	}
	rest := text[offset:]
	if len(rest) <= limit {
		return rest
	}
	end := len(textfmt.Prefix(rest, limit))
	return rest[:end] + fmt.Sprintf("\n... (truncated; fetch again with offset=%d to continue, %d bytes total)", offset+end, len(text))
}

// truncateText cuts s to at most limit bytes on a rune boundary, with a note.
func truncateText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	head := textfmt.Prefix(s, limit)
	return head + fmt.Sprintf("\n... (truncated at %d bytes of %d)", len(head), len(s))
}
