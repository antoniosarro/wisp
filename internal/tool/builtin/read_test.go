package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestReadTool(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "whole file", args: `{"path":%q}`, want: "     1\tone\n     2\ttwo\n     3\tthree\n     4\tfour"},
		{name: "offset skips lines", args: `{"path":%q,"offset":2}`, want: "     3\tthree\n     4\tfour"},
		{name: "limit caps lines", args: `{"path":%q,"limit":2}`, want: "     1\tone\n     2\ttwo\n... (file continues; read again with offset=2)"},
		{name: "offset and limit", args: `{"path":%q,"offset":1,"limit":1}`, want: "     2\ttwo\n... (file continues; read again with offset=2)"},
	}

	var rt ReadTool
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := json.RawMessage(fmt.Sprintf(tt.args, p))
			res, err := rt.Run(context.Background(), args)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Content != tt.want {
				t.Errorf("Content = %q, want %q", res.Content, tt.want)
			}
		})
	}
}

func TestReadToolMissingFile(t *testing.T) {
	var rt ReadTool
	_, err := rt.Run(context.Background(), json.RawMessage(`{"path":"/does/not/exist"}`))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestReadToolMissingPath(t *testing.T) {
	var rt ReadTool
	_, err := rt.Run(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error when path is omitted")
	}
}

func TestReadToolImage(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "board") // no extension: detected by content
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(fmt.Sprintf(`{"path":%q}`, p))

	res, err := ReadTool{Vision: visionOn()}.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Images) != 1 || res.Images[0].MIME != "image/png" || !bytes.Equal(res.Images[0].Data, buf.Bytes()) {
		t.Errorf("Images = %+v, want the PNG attached once", res.Images)
	}
	if !strings.Contains(res.Content, "attached") {
		t.Errorf("Content = %q, want a note that the image is attached", res.Content)
	}

	res, err = ReadTool{}.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("Run without vision: %v", err)
	}
	if len(res.Images) != 0 || !strings.Contains(res.Content, "cannot view images") {
		t.Errorf("without vision: Content = %q, Images = %d; want a note and no image", res.Content, len(res.Images))
	}
}

func TestReadToolLimits(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	read := func(args string) string {
		t.Helper()
		res, err := ReadTool{}.Run(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return res.Content
	}

	if got := read(fmt.Sprintf(`{"path":%q}`, write("bin", []byte("ELF\x00\x01garbage")))); !strings.Contains(got, "binary file") {
		t.Errorf("binary file = %q, want a note instead of its bytes", got)
	}
	huge := write("min.js", []byte(strings.Repeat("a", 2<<20)+"\nsecond\n"))
	got := read(fmt.Sprintf(`{"path":%q}`, huge))
	if len(got) > 3000 || !strings.Contains(got, "line truncated") || !strings.Contains(got, "2\tsecond") {
		t.Errorf("over-long line: %d bytes, want it cut and the next line still read", len(got))
	}
	many := write("many.txt", []byte(strings.Repeat(strings.Repeat("b", 100)+"\n", 2000)))
	got = read(fmt.Sprintf(`{"path":%q}`, many))
	if len(got) > maxReadBytes+200 || !strings.Contains(got, "read again with offset=") {
		t.Errorf("large output: %d bytes, want it capped with a continuation note", len(got))
	}
	if got := read(fmt.Sprintf(`{"path":%q}`, write("empty", nil))); got != "(empty file)" {
		t.Errorf("empty file = %q", got)
	}
	if got := read(fmt.Sprintf(`{"path":%q,"offset":10}`, write("short", []byte("x\n")))); !strings.Contains(got, "past the end") {
		t.Errorf("offset past end = %q", got)
	}
}

func visionOn() *atomic.Bool {
	var b atomic.Bool
	b.Store(true)
	return &b
}

// Opening a FIFO blocks until a writer appears: read must refuse it.
func TestReadRefusesFIFOs(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := ReadTool{}.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q}`, fifo)))
		if err != nil || !res.IsError || !strings.Contains(res.Content, "not a regular file") {
			t.Errorf("read of a FIFO = %+v, %v", res, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked on a FIFO")
	}
}
