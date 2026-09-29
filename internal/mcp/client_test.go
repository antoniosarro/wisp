package mcp

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// A server's configured headers go to its own host only: following a
// redirect elsewhere must not carry its token along.
func TestHeadersStayOnTheServersHost(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen["other"] = r.Header.Get("Authorization")
		mu.Unlock()
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen["server"] = r.Header.Get("Authorization")
		mu.Unlock()
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: headerTransport{headers: map[string]string{"Authorization": "Bearer secret"}, host: u.Host}}
	resp, err := client.Get(srv.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if seen["server"] != "Bearer secret" {
		t.Errorf("the server got %q, want its token", seen["server"])
	}
	if seen["other"] != "" {
		t.Errorf("the redirect target got %q: the token leaked", seen["other"])
	}
}
