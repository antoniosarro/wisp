package core

import (
	"context"
	"errors"
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

// Output caps of a suggestion. The first request asks for no reasoning
// and gets room for the message only. The retry, for backends that
// reason anyway or refuse to be asked not to, adds room for reasoning,
// bounded where the backend can bound it.
const (
	suggestCap          = 64
	suggestReasoningCap = 1024
)

// errSuggestOnlyReasoning is a suggestion response that reasoned without
// writing the message: cut off at its cap, or ended with no text.
var errSuggestOnlyReasoning = errors.New("the model only reasoned")

// SuggestNext asks provider for a likely next user message. For the
// backend to reuse its prompt cache, pass what a turn sends: history as
// Loop.Messages returns it, and tools only if the loop sends them (nil
// with NoTools). It never modifies history, so it can run while the
// caller keeps it.
//
// It asks the backend not to reason: a reasoning model would otherwise
// spend the small cap thinking and never write the message. When the
// backend rejects that request, or reasons anyway without answering, the
// request is sent once more with reasoning allowed but bounded.
func SuggestNext(ctx context.Context, provider model.Provider, tools *tool.Registry, history []model.Message) (string, error) {
	req := model.Request{
		// The full slice expression makes append copy: the caller's backing
		// array stays untouched.
		Messages:    append(history[:len(history):len(history)], model.Message{Role: model.RoleUser, Content: suggestPrompt}),
		MaxTokens:   suggestCap,
		NoReasoning: true,
	}
	if tools != nil {
		// Tools stay in the request so the prompt prefix matches the cache.
		req.Tools, req.ToolChoice = tools.Schemas(), "none"
	}
	text, err := suggestOnce(ctx, provider, req)
	if err != nil && ctx.Err() == nil && !errors.Is(err, model.ErrContextOverflow) {
		req.NoReasoning = false
		req.MaxTokens = suggestReasoningCap
		req.ReasoningTokens = suggestReasoningCap - suggestCap
		text, err = suggestOnce(ctx, provider, req)
	}
	if errors.Is(err, errSuggestOnlyReasoning) {
		return "", nil // no suggestion, which is not a failure
	}
	return text, err
}

// suggestOnce sends one suggestion request and returns the cleaned
// message, or errSuggestOnlyReasoning when the response has reasoning but
// no message.
func suggestOnce(ctx context.Context, provider model.Provider, req model.Request) (string, error) {
	events, err := provider.Stream(ctx, req)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	reasoned := false
	for e := range events {
		switch e.Kind {
		case model.EventTextDelta:
			text.WriteString(e.Text)
		case model.EventReasoningDelta:
			reasoned = true
		case model.EventReclassify:
			text.Reset()
			reasoned = true
		case model.EventError:
			return "", fmt.Errorf("suggestion: %w", e.Err)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(text.String()) == "" && reasoned {
		return "", errSuggestOnlyReasoning
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
