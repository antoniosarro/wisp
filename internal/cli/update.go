package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/version"
)

// releaseURL is GitHub's newest non-prerelease release of wisp.
var releaseURL = "https://api.github.com/repos/antoniosarro/wisp/releases/latest"

// releaseCheckEvery is how long an answer from GitHub is reused. A day was
// too long: a release made the day someone installed the one before it
// went unnoticed until the next day. Hourly stays far below GitHub's limit
// of 60 requests an hour without a token.
const releaseCheckEvery = time.Hour

// newerRelease returns the version of a release newer than current, or "".
// It asks GitHub at most once every releaseCheckEvery and remembers the
// answer in the state file; failing to ask (offline, no releases yet) just shows nothing.
// Builds not made from a release tag (go run's "dev", go build's and
// packages' 0.0.0 versions before the first tag) never check: there is
// nothing to compare.
func newerRelease(ctx context.Context, current string) string {
	cur := releaseParts(current)
	if !slices.ContainsFunc(cur, func(n int) bool { return n != 0 }) {
		return ""
	}
	s := loadState()
	if time.Since(s.ReleaseCheck) > releaseCheckEvery {
		if tag, err := latestRelease(ctx); err == nil {
			s = loadState() // the TUI may have saved it meanwhile
			s.LatestRelease, s.ReleaseCheck = tag, time.Now()
			saveState(s)
		}
	}
	if slices.Compare(releaseParts(s.LatestRelease), cur) > 0 {
		return strings.TrimPrefix(s.LatestRelease, "v")
	}
	return ""
}

// latestRelease asks GitHub for the newest release's tag.
func latestRelease(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release: %s", resp.Status)
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.Tag, nil
}

// releaseParts is the numeric release a version starts with: [1 2 3] for
// v1.2.3, 1.2.3, and 1.2.3.r4.gabc1234 (four commits past the tag, so not
// older than 1.2.3); none for "dev".
func releaseParts(v string) []int {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	var parts []int
	for f := range strings.SplitSeq(v, ".") {
		n, err := strconv.Atoi(f)
		if err != nil {
			break
		}
		parts = append(parts, n)
	}
	return parts
}
