package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// maxBashOutputBytes caps the output one bash call returns (tool.Clip).
var maxBashOutputBytes = tool.MaxOutputBytes

// maxBashCaptureBytes bounds the memory each output stream may use.
const maxBashCaptureBytes = 8 << 20

const (
	defaultBashTimeout = 2 * time.Minute
	maxBashTimeout     = 10 * time.Minute
)

// bashEnv keeps commands from blocking on pagers or credential prompts.
var bashEnv = []string{"PAGER=cat", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0"}

// BashTool runs shell commands. Each call is a fresh bash: no state
// carries over between calls except the files commands change.
type BashTool struct{}

func (BashTool) Schema() model.ToolSchema {
	return model.ToolSchema{
		Name:        "bash",
		Description: "Run a non-interactive shell command in the working directory and return its output. Stops after timeout seconds (default 120, max 600).",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "shell command to run"},
				"timeout": {"type": "integer", "description": "seconds before the command is stopped (default 120, max 600)"}
			},
			"required": ["command"]
		}`),
	}
}

func (BashTool) Risky() bool { return true }

// Run executes command via "bash -c". A non-zero exit or timeout is an
// IsError result with the output so far; a Go error means the command
// could not run or the turn was cancelled.
func (BashTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	a, err := decodeArgs[struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}](ctx, args)
	if err != nil {
		return tool.Result{}, err
	}
	if a.Command == "" {
		return tool.Result{}, fmt.Errorf("command is required")
	}

	timeout := defaultBashTimeout
	if a.Timeout > 0 {
		timeout = min(time.Duration(a.Timeout)*time.Second, maxBashTimeout)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-c", a.Command)
	cmd.Env = append(tool.ChildEnv(), bashEnv...)
	configureProcessGroup(cmd)
	// A background child holding the output pipes open would keep Run
	// waiting; after the command exits, give the pipes this long to drain.
	cmd.WaitDelay = 250 * time.Millisecond
	stdout, stderr := cappedBuffer{limit: maxBashCaptureBytes}, cappedBuffer{limit: maxBashCaptureBytes}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	// Nothing outlives the call: a background child would otherwise keep
	// running after wisp exits, unseen.
	leftover := ""
	if killProcessGroup(cmd) {
		leftover = "\n[stopped the background processes it left running; start long-lived servers outside wisp]"
	}

	if ctx.Err() != nil {
		return tool.Result{}, fmt.Errorf("running command: %w", ctx.Err())
	}
	if runCtx.Err() != nil {
		out := combineOutput(&stdout, &stderr, 0)
		return tool.Result{Content: strings.TrimLeft(out+"\n[timed out after "+timeout.String()+"]", "\n"), IsError: true}, nil
	}

	var exitErr *exec.ExitError
	switch {
	// ErrWaitDelay: the command succeeded, but a background child held
	// its output open.
	case runErr == nil || errors.Is(runErr, exec.ErrWaitDelay):
		return tool.Result{Content: strings.TrimLeft(combineOutput(&stdout, &stderr, 0)+leftover, "\n")}, nil
	case errors.As(runErr, &exitErr):
		return tool.Result{
			Content: strings.TrimLeft(combineOutput(&stdout, &stderr, exitErr.ExitCode())+leftover, "\n"),
			IsError: true,
		}, nil
	default:
		return tool.Result{}, fmt.Errorf("running command: %w", runErr)
	}
}

// cappedBuffer bounds memory while still draining child output: a writer
// that stopped accepting would block the command.
type cappedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	keep := min(n, max(0, b.limit-b.Len()))
	_, _ = b.Buffer.Write(p[:keep])
	b.truncated = b.truncated || keep < n
	return n, nil
}

func (b *cappedBuffer) String() string {
	if b.truncated {
		return b.Buffer.String() + "\n... (output truncated)"
	}
	return b.Buffer.String()
}

// combineOutput joins stdout, stderr, and a non-zero exit code into what
// the model reads, clipped to maxBashOutputBytes.
func combineOutput(stdout, stderr *cappedBuffer, exitCode int) string {
	var b strings.Builder
	writeSection(&b, stdout.String())
	if stderr.Len() > 0 {
		writeSection(&b, "[stderr]\n"+stderr.String())
	}
	if exitCode != 0 {
		writeSection(&b, fmt.Sprintf("[exit code %d]", exitCode))
	}

	captured := 0
	if stdout.truncated || stderr.truncated {
		captured = maxBashCaptureBytes
	}
	return tool.Clip(b.String(), maxBashOutputBytes, captured)
}

// writeSection appends s to b on a line of its own.
func writeSection(b *strings.Builder, s string) {
	if s == "" {
		return
	}
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	b.WriteString(s)
}
