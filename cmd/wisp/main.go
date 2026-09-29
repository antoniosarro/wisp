// wisp is a coding agent for the terminal (tui/cli).
package main

import (
	"os"

	// Built-in CA roots, used only when the system has none (e.g. minimal
	// containers); without them HTTPS to hosted providers fails.
	_ "golang.org/x/crypto/x509roots/fallback"
)

func main() { os.Exit(1) }
