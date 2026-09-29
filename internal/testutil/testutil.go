// Package testutil holds fakes shared by tests across packages. A fake
// only one package needs belongs in that package's own _test.go files.
package testutil

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// The fakes must keep satisfying the interfaces they stand in for, even
// before a test uses them.
var (
	_ model.Provider = (*ScriptedProvider)(nil)
	_ tool.Tool      = EchoTool{}
)

// ScriptedProvider replays one canned event sequence per Stream call, in
// order, and records what it was asked. It is not safe for concurrent use:
// read Calls and LastReq after the code under test is done.
type ScriptedProvider struct {
	Turns   [][]model.Event // one per Stream call
	Calls   int             // Stream calls so far
	LastReq model.Request   // the request of the latest Stream call
}

// Stream returns the next scripted turn on a closed, fully buffered
// channel, or an error once the script has run out, so a test that makes
// more requests than it expected fails instead of hanging.
func (p *ScriptedProvider) Stream(_ context.Context, req model.Request) (<-chan model.Event, error) {
	p.LastReq = req
	if p.Calls >= len(p.Turns) {
		return nil, errors.New("ScriptedProvider: no more turns scripted")
	}
	turn := p.Turns[p.Calls]
	p.Calls++

	ch := make(chan model.Event, len(turn))
	for _, e := range turn {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// EchoTool is a read-only tool named "echo" that returns its args.
type EchoTool struct{}

// Schema names the tool; it takes any arguments.
func (EchoTool) Schema() model.ToolSchema { return model.ToolSchema{Name: "echo"} }

// Risky is false, so calls never ask and run concurrently.
func (EchoTool) Risky() bool { return false }

// Run returns "echoed: " and the raw arguments.
func (EchoTool) Run(_ context.Context, args json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "echoed: " + string(args)}, nil
}
