package proto

import "encoding/json"

// OpenAI Chat Completions wire-format types — 仅本包 internal 用，不对外暴露。

type openAIChatRequest struct {
	Model               string           `json:"model"`
	Messages            []openAIChatMsg  `json:"messages"`
	Stream              *bool            `json:"stream"`
	MaxTokens           *int             `json:"max_tokens"`
	MaxCompletionTokens *int             `json:"max_completion_tokens"`
	Temperature         *float64         `json:"temperature"`
	TopP                *float64         `json:"top_p"`
	Stop                json.RawMessage  `json:"stop,omitempty"` // string 或 []string
	Tools               []openAIChatTool `json:"tools,omitempty"`
	ToolChoice          json.RawMessage  `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool            `json:"parallel_tool_calls"`
	ResponseFormat      json.RawMessage  `json:"response_format,omitempty"`
	Seed                *int             `json:"seed"`
	User                string           `json:"user,omitempty"`
	Store               *bool            `json:"store"`
	Metadata            map[string]any   `json:"metadata,omitempty"`
}

type openAIChatMsg struct {
	Role       string               `json:"role"`
	Content    json.RawMessage      `json:"content"`
	Name       string               `json:"name,omitempty"`
	ToolCalls  []openAIChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"` // role=tool 用
}

type openAIChatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded string
	} `json:"function"`
}

type openAIChatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type openAIChatContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL json.RawMessage `json:"image_url,omitempty"`
}
