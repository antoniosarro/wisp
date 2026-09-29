package builtin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fetchURL runs fetch on url with extra JSON fields, failing on a Go error.
func fetchURL(t *testing.T, f FetchTool, url, extra string) (string, bool) {
	t.Helper()
	res := run(t, f, fmt.Sprintf(`{"url":%q%s}`, url, extra))
	return res.Content, res.IsError
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><head><title>Docs</title><script>track()</script></head><body>
				<h1>Intro</h1><p>Hello &amp; welcome.</p><ul><li>one</li><li>two</li></ul></body></html>`)
		case "/data":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		case "/image":
			w.Header().Set("Content-Type", "image/png")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := FetchTool{Client: srv.Client()}

	if got, _ := fetchURL(t, f, srv.URL+"/page", ""); got != "Docs\n\n# Intro\n\nHello & welcome.\n\n- one\n- two" {
		t.Errorf("page = %q", got)
	}
	if got, _ := fetchURL(t, f, srv.URL+"/data", ""); got != `{"ok":true}` {
		t.Errorf("json = %q", got)
	}
	if got, isErr := fetchURL(t, f, srv.URL+"/missing", ""); !isErr || !strings.Contains(got, "404") {
		t.Errorf("404 = %q, error %v", got, isErr)
	}
	if got, isErr := fetchURL(t, f, srv.URL+"/image", ""); !isErr {
		t.Errorf("binary = %q, want an error result", got)
	}
	if got, _ := fetchURL(t, f, srv.URL+"/data", `,"max_bytes":4`); !strings.HasPrefix(got, `{"ok`) || !strings.Contains(got, "offset=4") {
		t.Errorf("truncated = %q, want the next offset", got)
	}
	if got, _ := fetchURL(t, f, srv.URL+"/data", `,"offset":4`); got != `":true}` {
		t.Errorf("continued = %q", got)
	}
	if runErr(f, `{"url":"file:///etc/passwd"}`) == nil {
		t.Error("non-http URL accepted")
	}
}

// A redirect to another host is reported, not followed: following it is a
// new call, approved for its own host, so an allowed host can't send the
// request to a local service or a cloud metadata address.
func TestFetchRedirectsStayOnHost(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "secret metadata")
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/away":
			http.Redirect(w, r, other.URL+"/latest/meta-data", http.StatusFound)
		default:
			w.Header().Set("Content-Type", "text/plain")
			_, _ = fmt.Fprint(w, "fine")
		}
	}))
	defer srv.Close()

	if got, _ := fetchURL(t, FetchTool{}, srv.URL+"/same", ""); !strings.Contains(got, "fine") {
		t.Errorf("same-host redirect not followed: %q", got)
	}
	if got, _ := fetchURL(t, FetchTool{}, srv.URL+"/away", ""); strings.Contains(got, "secret") || !strings.Contains(got, "redirects to "+other.URL+"/latest/meta-data") {
		t.Errorf("cross-host redirect: %q", got)
	}
}

func TestFetchLimits(t *testing.T) {
	big := strings.Repeat("x", 6<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(big))
		case "/untyped-text":
			w.Header()["Content-Type"] = nil
			_, _ = w.Write([]byte("hello plain text"))
		case "/untyped-binary":
			w.Header()["Content-Type"] = nil
			_, _ = w.Write([]byte{0x7f, 'E', 'L', 'F', 0, 0, 1, 2})
		case "/error":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(big))
		}
	}))
	defer srv.Close()

	if got, _ := fetchURL(t, FetchTool{}, srv.URL+"/big", `,"max_bytes":1000000000`); len(got) > maxFetchBytes+500 || !strings.Contains(got, "download stopped at 5 MB") {
		t.Errorf("big page: %d bytes, want at most %d plus notes including the download cutoff", len(got), maxFetchBytes)
	}
	if got, isErr := fetchURL(t, FetchTool{}, srv.URL+"/untyped-text", ""); isErr || got != "hello plain text" {
		t.Errorf("untyped text = %q, %v", got, isErr)
	}
	if got, isErr := fetchURL(t, FetchTool{}, srv.URL+"/untyped-binary", ""); !isErr || !strings.Contains(got, "not text") {
		t.Errorf("untyped binary = %q, %v; want it refused", got, isErr)
	}
	if got, isErr := fetchURL(t, FetchTool{}, srv.URL+"/error", ""); !isErr || len(got) > maxErrorText+200 {
		t.Errorf("error page: %d bytes, error %v", len(got), isErr)
	}
}

func TestPage(t *testing.T) {
	text := "héllo wörld"
	for _, c := range []struct {
		offset, limit int
		want          string
	}{
		{0, 100, text},
		{2, 100, "éllo wörld"}, // mid-character offset backs up to its start
		{0, 2, "h\n... (truncated; fetch again with offset=1 to continue, 13 bytes total)"},
		{99, 10, "(offset 99 is past the end of the 13-byte text)"},
	} {
		if got := page(text, c.offset, c.limit); got != c.want {
			t.Errorf("page(%d, %d) = %q, want %q", c.offset, c.limit, got, c.want)
		}
	}
}
