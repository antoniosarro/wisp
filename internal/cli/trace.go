package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/traceui"
)

// startTrace serves the trace UI for every directory's sessions in the
// background until ctx ends, and returns its URL, secret path included.
// If addr is taken, e.g. by another wisp with --trace, it takes a free
// port.
func startTrace(ctx context.Context, addr string) (string, error) {
	store, err := openStore()
	if err != nil {
		return "", err
	}
	here := store.Dir
	store.Dir = "" // the page shows every directory's sessions
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		host, _, _ := net.SplitHostPort(addr)
		if ln, err = net.Listen("tcp", net.JoinHostPort(host, "0")); err != nil {
			_ = store.Close()
			return "", err
		}
	}
	// A fresh secret each run: only the printed URL reads the transcripts.
	token := strings.ToLower(rand.Text())
	srv := &http.Server{Handler: traceui.Handler(store, here, token), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		_ = store.Close()
	}()
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "wisp: trace server: %v\n", err)
		}
	}()
	return "http://" + ln.Addr().String() + "/" + token + "/", nil
}

// serveTrace is --trace-only: the trace UI and nothing else, until ctx ends.
func serveTrace(ctx context.Context, addr string) error {
	url, err := startTrace(ctx, addr)
	if err != nil {
		return err
	}
	fmt.Printf("wisp trace: %s  (Ctrl-C to stop)\n", url)
	<-ctx.Done()
	return nil
}
