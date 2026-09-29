// Package version is wisp's release version
package version

import (
	"runtime/debug"
	"strings"
)

var Version = "dev"

func init() {
	if bi, ok := debug.ReadBuildInfo(); ok && Version == "dev" && strings.HasPrefix(bi.Main.Version, "v") {
		Version = strings.TrimPrefix(bi.Main.Version, "v")
	}
}

// UserAgent names wisp in HTTP requests.
func UserAgent() string { return "wisp/" + Version }
