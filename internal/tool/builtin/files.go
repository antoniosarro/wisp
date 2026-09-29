package builtin

import (
	"bufio"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/antoniosarro/wisp/internal/textfmt"
)

// maxLineBytes is the longest line read and grep examine; the rest of a
// longer line (minified code, data files) is skipped rather than failing.
const maxLineBytes = 1 << 20

// sniffBytes is how much of a file is looked at to tell binary from text,
// as http.DetectContentType does.
const sniffBytes = 512

// forEachLine calls fn with each line of r, without its line ending and
// cut to maxLineBytes, until fn returns false.
func forEachLine(r io.Reader, fn func(line string) bool) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	for {
		frag, isPrefix, err := br.ReadLine()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if room := maxLineBytes - len(buf); room > 0 {
			buf = append(buf, frag[:min(len(frag), room)]...)
		}
		if isPrefix {
			continue
		}
		if !fn(string(buf)) {
			return nil
		}
		buf = buf[:0]
	}
}

// snippet returns at most n bytes of line around byte offset at, with "…"
// marking each end that was cut.
func snippet(line string, at, n int) string {
	if len(line) <= n {
		return line
	}
	start := max(0, min(at-n/4, len(line)-n))
	for start > 0 && !utf8.RuneStart(line[start]) {
		start--
	}
	s := textfmt.Prefix(line[start:], n)
	end := start + len(s)
	if start > 0 {
		s = "…" + s
	}
	if end < len(line) {
		s += "…"
	}
	return s
}

// isBinary reports whether the start of a file looks binary: text files
// don't contain NUL bytes.
func isBinary(b []byte) bool {
	return bytes.IndexByte(b, 0) >= 0
}

// writeFileAtomic replaces the file at path with data through a temporary
// file and a rename, so a crash leaves the old or the new content, never
// half of it. A symlink is followed and its target replaced. perm applies
// to a new file; an existing file keeps its mode.
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		target = path // doesn't exist yet
	}
	if info, err := os.Stat(target); err == nil {
		perm = info.Mode().Perm()
	}
	// In the target's directory, so the rename stays on one filesystem.
	f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".wisp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() // no-op after the rename
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), target)
}
