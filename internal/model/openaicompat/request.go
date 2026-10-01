package openaicompat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/antoniosarro/wisp/internal/model"
)

// chatRequest is the body of POST /chat/completions.
type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	Tools         []chatTool     `json:"tools,omitempty"`
	ToolChoice    string         `json:"tool_choice,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	Provider      *routing       `json:"provider,omitempty"` // OpenRouter only
	Usage         *usageRequest  `json:"usage,omitempty"`    // OpenRouter only
	// Reasoning controls reasoning on OpenRouter, ReasoningEffort on
	// OpenAI-style APIs; ChatTemplateKwargs reaches it on llama.cpp, vLLM,
	// and SGLang, through the chat template.
	Reasoning          *reasoningRequest `json:"reasoning,omitempty"`
	ReasoningEffort    string            `json:"reasoning_effort,omitempty"`
	ChatTemplateKwargs map[string]any    `json:"chat_template_kwargs,omitempty"`
}

// reasoningRequest is OpenRouter's reasoning control: off, an effort
// level, or capped.
type reasoningRequest struct {
	Enabled   *bool  `json:"enabled,omitempty"`
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

// noThinking turns thinking off in the chat templates that support it:
// Qwen3 and most others read enable_thinking, DeepSeek V3.1 and Granite
// read thinking. Templates ignore variables they don't use.
var noThinking = map[string]any{"enable_thinking": false, "thinking": false}

// streamOptions asks for a final chunk carrying token usage.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role string `json:"role"`
	// Not omitempty: llama.cpp rejects messages without "content", even if empty.
	// A string, or []contentPart for a message carrying images.
	Content    any            `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// contentPart is one part of a multi-part message: text or an image.
type contentPart struct {
	Type     string    `json:"type"` // "text" or "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"` // a data: URL; nothing is uploaded elsewhere
}

type chatToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"` // always "function"
	Function chatToolCallFunction `json:"function"`
}

type chatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded args
}

type chatTool struct {
	Type     string       `json:"type"` // always "function"
	Function chatToolFunc `json:"function"`
}

type chatToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// imagesNote introduces the images that follow a run of tool results.
const imagesNote = "Images returned by the tool calls above:"

// toChatRequest translates req to the wire format, for modelName.
func toChatRequest(modelName string, req model.Request) chatRequest {
	messages := make([]chatMessage, 0, len(req.Messages))
	// Tool messages only carry text, so images from a run of tool results
	// follow it in one user message: tool results must stay adjacent to
	// the assistant message that called them.
	var pending []contentPart
	flush := func() {
		if len(pending) > 0 {
			messages = append(messages, chatMessage{Role: string(model.RoleUser), Content: pending})
			pending = nil
		}
	}
	for _, m := range req.Messages {
		if m.Role != model.RoleTool {
			flush()
		}
		for _, img := range m.Images {
			if len(pending) == 0 {
				pending = append(pending, contentPart{Type: "text", Text: imagesNote})
			}
			pending = append(pending, contentPart{Type: "image_url", ImageURL: &imageURL{
				URL: "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
			}})
		}
		cm := chatMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, chatToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: chatToolCallFunction{Name: tc.Name, Arguments: string(tc.Args)},
			})
		}
		messages = append(messages, cm)
	}
	flush()

	tools := make([]chatTool, len(req.Tools))
	for i, t := range req.Tools {
		tools[i] = chatTool{
			Type:     "function",
			Function: chatToolFunc{Name: t.Name, Description: t.Description, Parameters: t.Parameters},
		}
	}

	return chatRequest{
		Model:         modelName,
		Messages:      messages,
		Tools:         tools,
		ToolChoice:    req.ToolChoice,
		MaxTokens:     req.MaxTokens,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
}

// setReasoning applies req's reasoning controls the way the endpoint
// understands them. A reasoning budget only exists on OpenRouter. An
// effort level goes in reasoning_effort elsewhere, and on a local server
// also to the chat template, which reads reasoning_effort (gpt-oss) or,
// for "none", the variables of noThinking.
func (c *Client) setReasoning(chatReq *chatRequest, req model.Request) {
	switch {
	case req.NoReasoning && c.openRouter:
		off := false
		chatReq.Reasoning = &reasoningRequest{Enabled: &off}
	case req.NoReasoning:
		chatReq.ChatTemplateKwargs = noThinking
	case req.Effort != "" && c.openRouter:
		chatReq.Reasoning = &reasoningRequest{Effort: req.Effort}
	case req.Effort != "":
		chatReq.ReasoningEffort = req.Effort
		switch {
		case !isLocalURL(c.cfg.BaseURL):
		case req.Effort == "none":
			chatReq.ChatTemplateKwargs = noThinking
		default:
			chatReq.ChatTemplateKwargs = map[string]any{"reasoning_effort": req.Effort}
		}
	case req.ReasoningTokens > 0 && c.openRouter:
		chatReq.Reasoning = &reasoningRequest{MaxTokens: req.ReasoningTokens}
	}
}

// post starts a streaming chat completion, retrying failures that happen
// before the response starts (see retryable); the caller must close the
// body.
func (c *Client) post(ctx context.Context, req model.Request) (*http.Response, error) {
	chatReq := toChatRequest(c.Model(), req)
	c.setReasoning(&chatReq, req)
	if c.openRouter {
		c.route(ctx, &chatReq)
	}
	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("encoding chat request: %w", err)
	}
	for attempt := 0; ; attempt++ {
		httpReq, err := c.newRequest(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")

		resp, err := c.do(httpReq)
		if err == nil {
			return resp, nil
		}
		if attempt == len(retryDelays) || !retryable(err) {
			return nil, fmt.Errorf("chat request failed: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("chat request failed: %w", ctx.Err())
		case <-time.After(retryDelay(attempt, err)):
		}
	}
}
