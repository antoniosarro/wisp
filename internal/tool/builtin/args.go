package builtin

import (
	"context"
	"encoding/json"
	"fmt"
)

// decodeArgs checks ctx, then unmarshals a tool call's args into T.
func decodeArgs[T any](ctx context.Context, args json.RawMessage) (T, error) {
	var a T
	if err := ctx.Err(); err != nil {
		return a, err
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return a, fmt.Errorf("decoding args: %w", err)
	}
	return a, nil
}

// pathArg is a call's path argument, "." when absent. RiskyCall uses it,
// before Run has decoded the arguments; malformed args fail in Run.
func pathArg(args json.RawMessage) string {
	var a struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(args, &a)
	if a.Path == "" {
		return "."
	}
	return a.Path
}
