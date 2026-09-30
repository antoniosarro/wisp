//go:build !unix

package tui

import "os"

// NewInput returns f unchanged where split-sequence handling isn't supported.
func NewInput(f *os.File) *os.File { return f }
