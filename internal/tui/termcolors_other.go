//go:build !unix

package tui

import (
	"io"
	"os"
	"time"
)

// queryTermColors doesn't query: the mascot keeps its own colors.
func queryTermColors(*os.File, io.Writer, time.Duration) termColors { return termColors{} }
