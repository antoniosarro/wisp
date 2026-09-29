package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/tool"
)

// suggestPrompt asks for the user's likely next message.
const suggestPrompt = "Predict the user's most likely next message in this conversation. " +
	"Reply with only that message, written as the user, under 15 words, without quotes. " +
	"Do not call tools."

// maxSuggestionRunes is the longest reply taken as a chat message.
const maxSuggestionRunes = 120

// SuggestNext asks provider for a likely next user message. For the
// backend to reuse its prompt cache, pass what a turn sends: history as
// Loop.Messages returns it, and tools only if the loop sends them (nil
// with NoTools). It never modifies history, so it can run while the
// caller keeps it.
func SuggestNext(ctx context.Context, provider model.Provider, tools *tool.Registry, history []model.Message) (string, error) {
	req := model.Request{
		// The full slice expression makes append copy: the caller's backing
		// array stays untouched.
		Messages: append(history[:len(history):len(history)], model.Message{Role: model.RoleUser, Content: suggestPrompt}),
		// A short cap keeps a reasoning model from spending a long answer on it.
		MaxTokens: 64,
	}
	if tools != nil {
		// Tools stay in the request so the prompt prefix matches the cache.
		req.Tools, req.ToolChoice = tools.Schemas(), "none"
	}
	events, err := provider.Stream(ctx, req)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for e := range events {
		switch e.Kind {
		case model.EventTextDelta:
			text.WriteString(e.Text)
		case model.EventReclassify:
			text.Reset()
		case model.EventError:
			return "", fmt.Errorf("suggestion: %w", e.Err)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return cleanSuggestion(text.String()), nil
}

// cleanSuggestion keeps the first line, unquoted; overlong replies are
// dropped, since they are likely not a chat message.
func cleanSuggestion(s string) string {
	s = strings.TrimSpace(s)
	s, _, _ = strings.Cut(s, "\n")
	s = strings.Trim(strings.TrimSpace(s), `"'“”`)
	if len([]rune(s)) > maxSuggestionRunes {
		return ""
	}
	return s
}
