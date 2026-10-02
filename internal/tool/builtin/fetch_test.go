package builtin

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// fetchURL runs fetch on url with extra JSON fields, failing on a Go error.
func fetchURL(t *testing.T, f FetchTool, url, extra string) (string, bool) {
	t.Helper()
	res := run(t, f, fmt.Sprintf(`{"url":%q%s}`, url, extra))
	return res.Content, res.IsError
}

// localFetch can reach the loopback test servers, which the default client
// refuses.
var localFetch = FetchTool{Client: &http.Client{CheckRedirect: sameHostRedirect}}

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

	if got, _ := fetchURL(t, localFetch, srv.URL+"/same", ""); !strings.Contains(got, "fine") {
		t.Errorf("same-host redirect not followed: %q", got)
	}
	if got, _ := fetchURL(t, localFetch, srv.URL+"/away", ""); strings.Contains(got, "secret") || !strings.Contains(got, "redirects to "+other.URL+"/latest/meta-data") {
		t.Errorf("cross-host redirect: %q", got)
	}
}

// The default client refuses non-public addresses when it connects, whatever
// name led there.
func TestFetchRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "secret")
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	for _, u := range []string{srv.URL, "http://localhost:" + port} {
		_, err := FetchTool{}.Run(t.Context(), json.RawMessage(fmt.Sprintf(`{"url":%q}`, u)))
		if err == nil || !strings.Contains(err.Error(), "not a public address") {
			t.Errorf("%s: err = %v", u, err)
		}
	}

	for addr, refused := range map[string]bool{
		"127.0.0.1":              true,
		"::1":                    true,
		"0.0.0.0":                true,
		"10.1.2.3":               true,
		"172.16.0.1":             true,
		"192.168.1.1":            true,
		"169.254.169.254":        true,
		"100.100.100.200":        true,
		"fe80::1":                true,
		"fd00:ec2::254":          true,
		"::ffff:127.0.0.1":       true,
		"::ffff:169.254.169.254": true,
		"1.1.1.1":                false,
		"2606:4700::1111":        false,
	} {
		if err := checkAddr(netip.MustParseAddr(addr), nil); (err != nil) != refused {
			t.Errorf("%s: err = %v, want refused %v", addr, err, refused)
		}
	}
}

// --fetch-allow lets listed addresses through, and only those.
func TestFetchAllow(t *testing.T) {
	allow, err := ParseFetchAllow(" 192.168.1.0/24, 10.0.0.5,,::ffff:127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for addr, refused := range map[string]bool{
		"192.168.1.77":    false,
		"192.168.2.1":     true,
		"10.0.0.5":        false,
		"10.0.0.6":        true,
		"127.0.0.1":       false,
		"169.254.169.254": true,
	} {
		if err := checkAddr(netip.MustParseAddr(addr), allow); (err != nil) != refused {
			t.Errorf("%s: err = %v, want refused %v", addr, err, refused)
		}
	}
	if _, err := ParseFetchAllow("nas.lan"); err == nil {
		t.Error("a hostname was accepted")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "homelab")
	}))
	defer srv.Close()
	if got, _ := fetchURL(t, FetchTool{Client: NewFetchClient(allow)}, srv.URL, ""); got != "homelab" {
		t.Errorf("allowed address: %q", got)
	}
}

// A proxy from the environment is used, and trusted though it is local; the
// host it is asked for is checked before the request goes to it.
func TestFetchThroughProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "via proxy: "+r.URL.String())
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	f := FetchTool{Client: NewFetchClient(nil)}

	if got, _ := fetchURL(t, f, "http://1.1.1.1/page", ""); got != "via proxy: http://1.1.1.1/page" {
		t.Errorf("public host through the proxy: %q", got)
	}
	_, err := f.Run(t.Context(), json.RawMessage(`{"url":"http://10.0.0.1/"}`))
	if err == nil || !strings.Contains(err.Error(), "not a public address") {
		t.Errorf("private host through the proxy: err = %v", err)
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

	if got, _ := fetchURL(t, localFetch, srv.URL+"/big", `,"max_bytes":1000000000`); len(got) > maxFetchBytes+500 || !strings.Contains(got, "download stopped at 5 MB") {
		t.Errorf("big page: %d bytes, want at most %d plus notes including the download cutoff", len(got), maxFetchBytes)
	}
	if got, isErr := fetchURL(t, localFetch, srv.URL+"/untyped-text", ""); isErr || got != "hello plain text" {
		t.Errorf("untyped text = %q, %v", got, isErr)
	}
	if got, isErr := fetchURL(t, localFetch, srv.URL+"/untyped-binary", ""); !isErr || !strings.Contains(got, "not text") {
		t.Errorf("untyped binary = %q, %v; want it refused", got, isErr)
	}
	if got, isErr := fetchURL(t, localFetch, srv.URL+"/error", ""); !isErr || len(got) > maxErrorText+200 {
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
