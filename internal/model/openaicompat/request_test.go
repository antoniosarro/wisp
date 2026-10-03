package openaicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func TestPostSendsTheChatRequest(t *testing.T) {
	srv, got := chatServer(t)
	c := New(Config{BaseURL: srv.URL, Model: "qwen2.5-coder", APIKey: "secret"}, nil)
	body := postBody(t, c, got, model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
		Tools:    []model.ToolSchema{{Name: "read", Description: "read a file", Parameters: json.RawMessage(`{"type":"object"}`)}},
	})

	if _, header := got.get(); header.Get("Authorization") != "Bearer secret" {
		t.Errorf("Authorization = %q, want Bearer secret", header.Get("Authorization"))
	}
	var req chatRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}
	if req.Model != "qwen2.5-coder" || !req.Stream || req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
		t.Errorf("model %q, stream %v, stream options %+v", req.Model, req.Stream, req.StreamOptions)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content != "hello" {
		t.Errorf("Messages = %+v", req.Messages)
	}
	if len(req.Tools) != 1 || req.Tools[0].Type != "function" || req.Tools[0].Function.Name != "read" {
		t.Errorf("Tools = %+v", req.Tools)
	}
}

// llama.cpp rejects a request with HTTP 400 "All non-assistant messages
// must contain 'content'" when a tool result's empty content is omitted,
// as `json:"content,omitempty"` would: content must always be present.
func TestEmptyContentIsSent(t *testing.T) {
	body, _ := json.Marshal(toChatRequest("m", model.Request{Messages: []model.Message{
		{Role: model.RoleTool, Content: "", ToolCallID: "call_1"},
	}}))
	if !strings.Contains(string(body), `"content":""`) {
		t.Errorf("request body = %s, want an explicit empty \"content\" field", body)
	}
}

func TestToolChoiceOnlySentWhenSet(t *testing.T) {
	body, _ := json.Marshal(toChatRequest("m", model.Request{}))
	if strings.Contains(string(body), "tool_choice") {
		t.Errorf("default request sends tool_choice: %s", body)
	}
	body, _ = json.Marshal(toChatRequest("m", model.Request{ToolChoice: "required"}))
	if !strings.Contains(string(body), `"tool_choice":"required"`) {
		t.Errorf("required tool choice missing: %s", body)
	}
}

func TestToolCallsAreSentAsFunctions(t *testing.T) {
	req := toChatRequest("m", model.Request{Messages: []model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "a", Name: "read", Args: json.RawMessage(`{"path":"x"}`)}}},
	}})
	want := chatToolCall{ID: "a", Type: "function", Function: chatToolCallFunction{Name: "read", Arguments: `{"path":"x"}`}}
	if calls := req.Messages[0].ToolCalls; len(calls) != 1 || calls[0] != want {
		t.Errorf("tool calls = %+v, want %+v", calls, want)
	}
}

func TestImagesFollowToolResults(t *testing.T) {
	img := model.Image{MIME: "image/png", Data: []byte{1, 2, 3}}
	req := toChatRequest("m", model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "look"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "a", Name: "read"}, {ID: "b", Name: "read"}}},
		{Role: model.RoleTool, ToolCallID: "a", Content: "a.png attached", Images: []model.Image{img}},
		{Role: model.RoleTool, ToolCallID: "b", Content: "b.txt text"},
		{Role: model.RoleAssistant, Content: "done"},
	}})

	var roles []string
	for _, m := range req.Messages {
		roles = append(roles, m.Role)
	}
	if got, want := strings.Join(roles, ","), "user,assistant,tool,tool,user,assistant"; got != want {
		t.Fatalf("roles = %s, want %s: tool results must stay adjacent, images after them", got, want)
	}
	if req.Messages[2].Content != "a.png attached" {
		t.Errorf("tool content = %v, want the text only", req.Messages[2].Content)
	}
	parts, ok := req.Messages[4].Content.([]contentPart)
	if !ok || len(parts) != 2 || parts[0].Text != imagesNote || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/png;base64,AQID" {
		t.Errorf("image message content = %+v, want the note and the data URL", req.Messages[4].Content)
	}
}

// A user's pasted images go in the user's own message, after its text.
func TestUserImagesStayInTheirMessage(t *testing.T) {
	img := model.Image{MIME: "image/png", Data: []byte{1, 2, 3}}
	req := toChatRequest("m", model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "what is this? [Image #1]", Images: []model.Image{img}},
	}})
	if len(req.Messages) != 1 {
		t.Fatalf("%d messages, want 1", len(req.Messages))
	}
	parts, ok := req.Messages[0].Content.([]contentPart)
	if !ok || len(parts) != 2 || parts[0].Text != "what is this? [Image #1]" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/png;base64,AQID" {
		t.Errorf("content = %+v, want the text and the data URL", req.Messages[0].Content)
	}
}

func TestReasoningControls(t *testing.T) {
	srv, got := chatServer(t)
	httpClient := redirectClient(srv)
	for _, c := range []struct {
		name        string
		baseURL     string
		noReasoning bool
		budget      int
		effort      string
		want        string // "" means no reasoning fields at all
	}{
		{"off through the chat template", "http://localhost:8091/v1", true, 0, "", `"chat_template_kwargs":{"enable_thinking":false,"thinking":false}`},
		{"off on OpenRouter", "http://openrouter.ai/api/v1", true, 0, "", `"reasoning":{"enabled":false}`},
		{"off wins over an effort", "http://openrouter.ai/api/v1", true, 0, "high", `"reasoning":{"enabled":false}`},
		{"budget on OpenRouter", "http://openrouter.ai/api/v1", false, 3000, "", `"reasoning":{"max_tokens":3000}`},
		{"budget elsewhere is not sent", "http://localhost:8091/v1", false, 3000, "", ""},
		{"effort on OpenRouter", "http://openrouter.ai/api/v1", false, 0, "xhigh", `"reasoning":{"effort":"xhigh"}`},
		{"effort on a hosted API", "http://opencode.ai/zen/v1", false, 0, "max", `"reasoning_effort":"max"}`},
		{"effort on a local server", "http://localhost:8091/v1", false, 0, "low", `"reasoning_effort":"low","chat_template_kwargs":{"reasoning_effort":"low"}`},
		{"effort none on a local server", "http://localhost:8091/v1", false, 0, "none", `"reasoning_effort":"none","chat_template_kwargs":{"enable_thinking":false,"thinking":false}`},
		{"default", "http://localhost:8091/v1", false, 0, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			client := New(Config{BaseURL: c.baseURL, Model: "m"}, httpClient)
			body := postBody(t, client, got, model.Request{NoReasoning: c.noReasoning, ReasoningTokens: c.budget, Effort: c.effort})
			if c.want == "" && (strings.Contains(body, "chat_template_kwargs") || strings.Contains(body, `"reasoning`)) {
				t.Errorf("body = %s, want reasoning left to the server", body)
			}
			if c.want != "" && !strings.Contains(body, c.want) {
				t.Errorf("body = %s, want %s", body, c.want)
			}
		})
	}
}
