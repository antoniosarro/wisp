package traceui

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// localOnly refuses requests that don't come from a page on this machine.
// The server listens on localhost, but a website can still reach it through
// DNS rebinding (its name resolving to 127.0.0.1): the Host header then
// carries the website's name. Origin catches cross-site requests that
// don't need a preflight.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Host header is the client's word; the connection's own
		// address is not. Both must be local, even on --trace-addr 0.0.0.0.
		if !isLocal(r.RemoteAddr) {
			http.Error(w, "forbidden client", http.StatusForbidden)
			return
		}
		if !isLocal(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// The secret path must not leak to other sites through a Referer.
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// isLocal reports whether hostport names this machine: localhost or a
// loopback address.
func isLocal(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
