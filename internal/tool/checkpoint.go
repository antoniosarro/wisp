package tool

import "context"

// checkpointKey is the context key a turn's checkpoint function is stored
// under.
type checkpointKey struct{}

// WithCheckpoint returns ctx carrying save, which tools that change files
// call with a file's path before changing it, so the change can be undone.
func WithCheckpoint(ctx context.Context, save func(path string) error) context.Context {
	return context.WithValue(ctx, checkpointKey{}, save)
}

// Checkpoint saves path's content before a tool changes it, through the
// function ctx carries; without one it does nothing.
func Checkpoint(ctx context.Context, path string) error {
	if save, _ := ctx.Value(checkpointKey{}).(func(string) error); save != nil {
		return save(path)
	}
	return nil
}
