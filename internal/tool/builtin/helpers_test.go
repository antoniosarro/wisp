package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/antoniosarro/wisp/internal/tool"
)

// run calls tl with args and fails the test on a Go error.
func run(t *testing.T, tl tool.Tool, args string) tool.Result {
	t.Helper()
	res, err := tl.Run(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Run(%s): %v", args, err)
	}
	return res
}

// runErr calls tl with args and returns only its Go error.
func runErr(tl tool.Tool, args string) error {
	_, err := tl.Run(context.Background(), json.RawMessage(args))
	return err
}

// jsonArgs encodes a tool call's arguments.
func jsonArgs(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// writeTree creates each file under dir, with its parent directories.
func writeTree(t *testing.T, dir string, files ...string) {
	t.Helper()
	for _, f := range files {
		writeFile(t, filepath.Join(dir, f), "x")
	}
}

// writeFile writes content to path, creating its parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// equalUnordered reports whether a and b hold the same strings.
func equalUnordered(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}
