package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewerRelease(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		_, _ = w.Write([]byte(`{"tag_name": "v1.3.0"}`))
	}))
	defer srv.Close()
	setReleaseURL(t, srv.URL)

	for _, tc := range []struct{ current, want string }{
		{"dev", ""},                // go run: never asks
		{"0.0.0.r15.gabc1234", ""}, // before the first tag
		{"0.0.0-abc1234-dirty", ""},
		{"0.0.0-20260929083344-013debf7a7df+dirty", ""},   // go build before the first tag
		{"1.2.10-0.20260929083344-013debf7a7df", "1.3.0"}, // go build past v1.2.9
		{"1.2.9", "1.3.0"},
		{"1.2.9.r4.gabc1234", "1.3.0"},
		{"1.3.0", ""},
		{"1.3.0.r2.gabc1234", ""}, // past the release
		{"1.10.0", ""},            // numeric, not string, order
	} {
		if got := newerRelease(context.Background(), tc.current); got != tc.want {
			t.Errorf("newerRelease(%q) = %q, want %q", tc.current, got, tc.want)
		}
	}
	if asked != 1 {
		t.Errorf("asked GitHub %d times, want once a day", asked)
	}
}

func TestNewerReleaseIgnoresFailures(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	srv := httptest.NewServer(http.NotFoundHandler()) // no releases yet
	defer srv.Close()
	setReleaseURL(t, srv.URL)
	if got := newerRelease(context.Background(), "1.0.0"); got != "" {
		t.Errorf("newerRelease = %q without a release", got)
	}
	if s := loadState(); !s.ReleaseCheck.IsZero() {
		t.Error("a failed check was recorded, so it would not retry")
	}
}

// setReleaseURL points the check at url for the rest of the test.
func setReleaseURL(t *testing.T, url string) {
	t.Helper()
	orig := releaseURL
	releaseURL = url
	t.Cleanup(func() { releaseURL = orig })
}
