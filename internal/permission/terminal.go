package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/antoniosarro/wisp/internal/termsafe"
)

// TerminalPrompter asks on a plain terminal, for one-shot runs: it prompts
// on Out (default stdout) and reads y/a/N from In (default stdin).
// Anything else, including EOF, denies.
type TerminalPrompter struct {
	In  io.Reader
	Out io.Writer
}

// Prompt asks for a call of the main agent.
func (p TerminalPrompter) Prompt(name string, args json.RawMessage) Decision {
	return p.ask("", name, args)
}

// PromptContext returns Deny as soon as ctx is done. The read it started
// keeps waiting for a line, so the next line typed goes to it and is
// dropped: the answer is lost, never applied to another call.
func (p TerminalPrompter) PromptContext(ctx context.Context, name string, args json.RawMessage) Decision {
	if ctx.Err() != nil {
		return Deny
	}
	reply := make(chan Decision, 1)
	go func() { reply <- p.ask(Origin(ctx), name, args) }()
	select {
	case d := <-reply:
		return d
	case <-ctx.Done():
		return Deny
	}
}

// ask prints the call, with control characters shown rather than acted on
// (termsafe.Show), and reads one line of answer. The rule label needs it as
// much as the arguments: for bash it is built from the decoded command.
func (p TerminalPrompter) ask(origin, name string, args json.RawMessage) Decision {
	out := p.Out
	if out == nil {
		out = os.Stdout
	}
	in := p.In
	if in == nil {
		in = os.Stdin
	}

	who := "wisp"
	if origin != "" {
		who = "sub-agent " + origin
	}
	_, _ = fmt.Fprintf(out, "%s wants to run: %s(%s)\n[y] allow  [a] always allow %s  [N] deny: ", who, name, termsafe.Show(string(args)), termsafe.Show(RuleLabel(name, args)))

	switch strings.TrimSpace(strings.ToLower(readLine(in))) {
	case "y", "yes":
		return Allow
	case "a", "always":
		return AllowAlways
	}
	return Deny
}

// readLine reads up to a newline or the end of in, a byte at a time: a
// buffered reader could swallow the answers to later prompts from piped
// stdin.
func readLine(in io.Reader) string {
	var line strings.Builder
	var b [1]byte
	for {
		n, err := in.Read(b[:])
		if n > 0 {
			if b[0] == '\n' {
				break
			}
			line.WriteByte(b[0])
		}
		if err != nil {
			break
		}
	}
	return line.String()
}
