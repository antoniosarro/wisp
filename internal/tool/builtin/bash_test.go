package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBashToolSuccess(t *testing.T) {
	var bt BashTool
	res, err := bt.Run(context.Background(), json.RawMessage(`{"command":"echo hi"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Errorf("IsError = true, want false")
	}
	if strings.TrimSpace(res.Content) != "hi" {
		t.Errorf("Content = %q, want %q", res.Content, "hi")
	}
}

func TestBashToolNonZeroExit(t *testing.T) {
	var bt BashTool
	res, err := bt.Run(context.Background(), json.RawMessage(`{"command":"echo oops >&2; exit 3"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatal("IsError = false, want true for a non-zero exit")
	}
	if !strings.Contains(res.Content, "[stderr]\noops") || !strings.Contains(res.Content, "[exit code 3]") {
		t.Errorf("Content = %q, want stderr and exit code noted", res.Content)
	}
}

func TestBashToolMissingCommand(t *testing.T) {
	var bt BashTool
	_, err := bt.Run(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error when command is omitted")
	}
}

func TestBashToolContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	var bt BashTool
	_, err := bt.Run(ctx, json.RawMessage(`{"command":"sleep 5"}`))
	if err == nil {
		t.Fatal("expected an error when the context times out")
	}
}

func TestBashToolTruncatesOutput(t *testing.T) {
	orig := maxBashOutputBytes
	maxBashOutputBytes = 10
	defer func() { maxBashOutputBytes = orig }()

	var bt BashTool
	res, err := bt.Run(context.Background(), json.RawMessage(`{"command":"echo 0123456789abcdef"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, after, ok := strings.Cut(res.Content, "full output saved to ")
	path, _, _ := strings.Cut(after, ";")
	if !ok || !strings.Contains(res.Content, "bytes omitted") || !strings.HasSuffix(strings.TrimSpace(res.Content), "abcdef") {
		t.Fatalf("Content = %q, want head, tail, and a note naming the saved file", res.Content)
	}
	defer func() { _ = os.Remove(path) }()
	if full, err := os.ReadFile(path); err != nil || string(full) != "0123456789abcdef\n" {
		t.Errorf("saved output = %q, %v", full, err)
	}
}

func TestBashToolTimeoutReturnsPartialOutput(t *testing.T) {
	start := time.Now()
	res, err := BashTool{}.Run(context.Background(), json.RawMessage(`{"command":"echo started; sleep 5","timeout":1}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "started") || !strings.Contains(res.Content, "timed out after 1s") {
		t.Fatalf("result = %+v, want partial output and a timeout note", res)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout took %v", time.Since(start))
	}
}

func TestBashToolDisablesPagers(t *testing.T) {
	res, err := BashTool{}.Run(context.Background(), json.RawMessage(`{"command":"echo $GIT_PAGER"}`))
	if err != nil || strings.TrimSpace(res.Content) != "cat" {
		t.Fatalf("GIT_PAGER = %q, %v", res.Content, err)
	}
}

func TestBashBackgroundChild(t *testing.T) {
	start := time.Now()
	res, err := BashTool{}.Run(context.Background(), json.RawMessage(`{"command":"echo hi; sleep 30 & echo $!"}`))
	if err != nil {
		t.Fatalf("a background child lost the output: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("waited for the background child")
	}
	lines := strings.Split(res.Content, "\n")
	if lines[0] != "hi" || !strings.Contains(res.Content, "stopped the background processes") {
		t.Fatalf("content = %q", res.Content)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat("/proc/" + strings.TrimSpace(lines[1])); err == nil {
		t.Error("the background child outlived the call")
	}
}

func TestBashDoesNotSeeAPIKey(t *testing.T) {
	t.Setenv("WISP_API_KEY", "sk-secret")
	res, err := BashTool{}.Run(context.Background(), json.RawMessage(`{"command":"echo ${WISP_API_KEY:-none}"}`))
	if err != nil || strings.TrimSpace(res.Content) != "none" {
		t.Errorf("bash saw the key: %q, %v", res.Content, err)
	}
}

func TestBashCancellationKillsChildren(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process group cancellation currently Linux-only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (BashTool{}).Run(ctx, json.RawMessage(`{"command":"sleep 2; echo should-not-run"}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancel waited for child process")
	}
}

func TestCappedBufferKeepsDraining(t *testing.T) {
	b := cappedBuffer{limit: 16}
	for range 100 {
		if n, err := b.Write([]byte("0123456789")); n != 10 || err != nil {
			t.Fatal("writer failed to drain")
		}
	}
	if b.Len() != 16 || !b.truncated || !strings.HasSuffix(b.String(), "(output truncated)") {
		t.Fatalf("buffer not bounded: %d bytes, truncated %v", b.Len(), b.truncated)
	}
}
