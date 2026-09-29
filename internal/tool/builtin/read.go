package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/textfmt"
	"github.com/antoniosarro/wisp/internal/tool"
)

const (
	defaultReadLimit = 2000     // lines returned when the call sets no limit
	maxImageBytes    = 10 << 20 // larger images are refused, not attached
	maxReadLineBytes = 2000     // longer lines are cut, with a note
	maxReadBytes     = 50_000   // output cap; the rest is left for another read
)

// imageTypes are the formats vision models accept.
var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// ReadTool reads text files. While Vision is set, image files are attached
// to the result for the model to see; otherwise they are reported, not
// dumped as bytes. Vision is shared so switching models can toggle it.
type ReadTool struct {
	Vision *atomic.Bool // nil means off
}

// vision reports whether images are shown to the model right now.
func (t ReadTool) vision() bool { return t.Vision != nil && t.Vision.Load() }

func (t ReadTool) Schema() model.ToolSchema {
	desc := "Read a file, optionally a line range. Each output line is prefixed with its line number and a tab; the prefix is not part of the file. Output stops at 2000 lines or 50 KB and lines over 2000 bytes are cut, with a note giving the offset to continue from."
	if t.vision() {
		desc += " Image files (PNG, JPEG, GIF, WebP) are shown to you as images."
	}
	return model.ToolSchema{
		Name:        "read",
		Description: desc,
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "file path to read"},
				"offset": {"type": "integer", "description": "number of lines to skip from the start"},
				"limit": {"type": "integer", "description": "maximum lines to return (default 2000)"}
			},
			"required": ["path"]
		}`),
	}
}

func (ReadTool) Risky() bool { return false }

// RiskyCall asks before reading credentials.
func (ReadTool) RiskyCall(args json.RawMessage) bool { return sensitivePath(pathArg(args)) }

func (t ReadTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	if a.Path == "" {
		return tool.Result{}, fmt.Errorf("path is required")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}

	// Opening a FIFO, or reading a terminal, would block the call.
	if info, err := os.Stat(a.Path); err == nil && !info.Mode().IsRegular() {
		kind := "not a regular file"
		if info.IsDir() {
			kind = "a directory; use ls"
		}
		return tool.Result{Content: a.Path + " is " + kind, IsError: true}, nil
	}
	f, err := os.Open(a.Path)
	if err != nil {
		return tool.Result{}, fmt.Errorf("opening %s: %w", a.Path, err)
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, sniffBytes)
	n, _ := f.Read(head)
	if mime := http.DetectContentType(head[:n]); imageTypes[mime] {
		return t.readImage(f, a.Path, mime)
	}
	if isBinary(head[:n]) {
		size := "unknown"
		if info, err := f.Stat(); err == nil {
			size = textfmt.Size(info.Size())
		}
		return tool.Result{Content: fmt.Sprintf("%s is a binary file (%s); its contents are not shown.", a.Path, size)}, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return tool.Result{}, fmt.Errorf("reading %s: %w", a.Path, err)
	}

	var b strings.Builder
	lineNum, shown, more := 0, 0, false
	err = forEachLine(f, func(line string) bool {
		lineNum++
		if lineNum <= a.Offset {
			return ctx.Err() == nil
		}
		if len(line) > maxReadLineBytes {
			line = snippet(line, 0, maxReadLineBytes) + fmt.Sprintf(" (line truncated at %d bytes)", maxReadLineBytes)
		}
		if shown >= limit || b.Len()+len(line) > maxReadBytes {
			more = true
			return false
		}
		if shown > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%6d\t%s", lineNum, line)
		shown++
		return ctx.Err() == nil
	})
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if err != nil {
		return tool.Result{}, fmt.Errorf("reading %s: %w", a.Path, err)
	}

	switch {
	case more:
		fmt.Fprintf(&b, "\n... (file continues; read again with offset=%d)", lineNum-1)
	case lineNum == 0:
		return tool.Result{Content: "(empty file)"}, nil
	case shown == 0:
		return tool.Result{Content: fmt.Sprintf("(offset %d is past the end of the file, which has %d lines)", a.Offset, lineNum)}, nil
	}
	return tool.Result{Content: b.String()}, nil
}

// readImage attaches the image open as f, checking its size before
// reading it.
func (t ReadTool) readImage(f *os.File, path, mime string) (tool.Result, error) {
	if !t.vision() {
		return tool.Result{Content: fmt.Sprintf("%s is a %s image; this model cannot view images.", path, mime)}, nil
	}
	info, err := f.Stat()
	if err != nil {
		return tool.Result{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if info.Size() > maxImageBytes {
		return tool.Result{}, fmt.Errorf("%s is a %d-byte image, over the %d-byte limit", path, info.Size(), maxImageBytes)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return tool.Result{}, fmt.Errorf("reading %s: %w", path, err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return tool.Result{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return tool.Result{
		Content: fmt.Sprintf("%s (%s, %d bytes) is attached as an image.", path, mime, len(data)),
		Images:  []model.Image{{MIME: mime, Data: data}},
	}, nil
}
