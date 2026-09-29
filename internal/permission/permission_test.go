package permission

import (
	"context"
	"testing"
)

func TestOrigin(t *testing.T) {
	if got := Origin(context.Background()); got != "" {
		t.Errorf("Origin without a sub-agent = %q, want the main agent's \"\"", got)
	}
	if got := Origin(WithOrigin(context.Background(), "explorer")); got != "explorer" {
		t.Errorf("Origin = %q, want explorer", got)
	}
}
