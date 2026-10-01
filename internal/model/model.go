// Package model defines the provider-agnostic contract between the core
// loop and an LLM backend: the loop sends a Request, a Provider streams
// back Events, and a Catalog, when the backend has one, lists and switches
// models. Backends translate to and from these types; nothing here knows
// a wire format.
package model

import (
	"context"
	"encoding/json"
	"errors"
)

// Role says who wrote a Message.
type Role string

const (
	RoleSystem    Role = "system"    // the system prompt
	RoleUser      Role = "user"      // the user's messages
	RoleAssistant Role = "assistant" // the model's replies and tool calls
	RoleTool      Role = "tool"      // a tool's result, answering one ToolCall
)

// Message is one turn in the conversation sent to a provider.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // set on assistant messages that requested tools
	ToolCallID string     // set on tool-role messages, links back to the call
	IsError    bool       // set on tool-role messages whose call failed
	Images     []Image    `json:"-"` // set on tool-role messages whose tool returned images; not persisted

	// Elided, when set, is sent instead of Content: a short stand-in for old
	// tool output trimmed to save context. Content keeps the full text.
	Elided string
}

// Image is an encoded image (PNG, JPEG, GIF, or WebP) for a vision model.
type Image struct {
	MIME string // e.g. "image/png"
	Data []byte
}

// ToolSchema describes a callable tool to the model.
type ToolSchema struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON schema
}

// ToolCall is a single invocation the model requested.
type ToolCall struct {
	ID   string // the provider's ID, echoed back in the result's ToolCallID
	Name string
	Args json.RawMessage

	// Elided, when set, is sent instead of Args: the arguments with their
	// large strings (a written file's body) replaced by a short stand-in.
	// Args keeps the full text.
	Elided json.RawMessage `json:",omitempty"`
}

// Request is one turn's worth of context sent to a provider.
type Request struct {
	Messages   []Message
	Tools      []ToolSchema
	ToolChoice string // "" lets the model decide; "none" forbids tool calls
	MaxTokens  int    // output-token cap; 0 leaves it to the backend
	// NoReasoning asks the backend to answer without thinking first, where
	// it has a way to say so. Backends without one may reject the request.
	NoReasoning bool
	// ReasoningTokens, if set, caps the tokens spent reasoning, where the
	// backend has a way to say so; MaxTokens still caps the whole output.
	ReasoningTokens int
	// Effort is the reasoning effort level (see Efforts), "" for the
	// model's default; NoReasoning overrides it.
	Effort string
}

// Provider streams a model response for a Request, normalizing
// backend-specific reasoning formats into Event.Reasoning.
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

// ErrContextOverflow is wrapped by provider errors caused by a request
// that no longer fits the model's context window.
var ErrContextOverflow = errors.New("context window exceeded")
