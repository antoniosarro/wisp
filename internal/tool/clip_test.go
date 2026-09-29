package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestClipIsUTF8SafeAndPrunes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	old := filepath.Join(tmp, "wisp-output-old.txt")
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * spillMaxAge)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}

	out := Clip(strings.Repeat("é", MaxOutputBytes), MaxOutputBytes, 8<<20) // no newlines at all
	if !utf8.ValidString(out) {
		t.Error("clipped output is not valid UTF-8")
	}
	if !strings.Contains(out, "itself cut at 8 MB") {
		t.Errorf("capped output note missing: %q", out[len(out)/3:len(out)/3+200])
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("stale saved output was not pruned")
	}
}
