package tui

import "github.com/antoniosarro/wisp/internal/termsafe"

// sanitize keeps model and tool text from driving the terminal (package
// termsafe). Everything the transcript shows goes through it at render
// time, so no path into the transcript can skip it.
var sanitize = termsafe.Strip
