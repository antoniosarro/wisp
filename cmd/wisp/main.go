// Command wisp is a general-purpose agent for the terminal (internal/cli).
package main

import (
	"os"

	// Built-in CA roots, used only when the system has none (e.g. minimal
	// containers); without them HTTPS to hosted providers fails.
	_ "golang.org/x/crypto/x509roots/fallback"

	"github.com/antoniosarro/wisp/internal/cli"
)

func main() { os.Exit(cli.Main()) }
