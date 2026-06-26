package proto

import "encoding/json"

type openAIResponsesRequest struct {
	Model                string            `json:"model"`
	Input                json.RawMessage   `json:"input"` // string 或 array
	Instructions         string            `json:"instructions,omitempty"`
	Stream               *bool             `json:"stream"`
	MaxOutputTokens      *int              `json:"max_output_tokens"`
	Temperature          *float64          `json:"temperature"`
	TopP                 *float64          `json:"top_p"`
	Tools                []json.RawMessage `json:"tools,omitempty"`
	ToolChoice           json.RawMessage   `json:"tool_choice,omitempty"`
	ParallelToolCalls    *bool             `json:"parallel_tool_calls"`
	Text                 json.RawMessage   `json:"text,omitempty"`
	Reasoning            json.RawMessage   `json:"reasoning,omitempty"`
	TopLogprobs          json.RawMessage   `json:"top_logprobs,omitempty"`
	MaxToolCalls         json.RawMessage   `json:"max_tool_calls,omitempty"`
	Include              json.RawMessage   `json:"include,omitempty"`
	Conversation         json.RawMessage   `json:"conversation,omitempty"`
	ContextManagement    json.RawMessage   `json:"context_management,omitempty"`
	PromptCacheKey       json.RawMessage   `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention json.RawMessage   `json:"prompt_cache_retention,omitempty"`
	Truncation           json.RawMessage   `json:"truncation,omitempty"`
	User                 json.RawMessage   `json:"user,omitempty"`
	EnableThinking       json.RawMessage   `json:"enable_thinking,omitempty"`
	Preset               json.RawMessage   `json:"preset,omitempty"`
	Store                *bool             `json:"store"`
	Metadata             map[string]any    `json:"metadata,omitempty"`
	PreviousResponseID   string            `json:"previous_response_id,omitempty"`
}

type openAIResponsesInputItem struct {
	Type             string                         `json:"type"`
	ID               string                         `json:"id,omitempty"`                // reasoning 项
	Status           string                         `json:"status,omitempty"`            // reasoning 项
	EncryptedContent string                         `json:"encrypted_content,omitempty"` // reasoning 项
	Summary          []openAIResponsesReasoningPart `json:"summary,omitempty"`           // reasoning 项
	Role             string                         `json:"role,omitempty"`              // message 项
	Content          json.RawMessage                `json:"content,omitempty"`           // message / reasoning 项
	CallID           string                         `json:"call_id,omitempty"`           // function_call / function_call_output
	Name             string                         `json:"name,omitempty"`              // function_call
	Arguments        string                         `json:"arguments,omitempty"`         // function_call
	Output           string                         `json:"output,omitempty"`            // function_call_output 用
}

type openAIResponsesReasoningPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type openAIResponsesInputPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL json.RawMessage `json:"image_url,omitempty"`
	Source   json.RawMessage `json:"source,omitempty"`
	Detail   string          `json:"detail,omitempty"`
}

type openAIResponsesResponse struct {
	ID                string                     `json:"id"`
	Object            string                     `json:"object"`
	Model             string                     `json:"model"`
	Status            string                     `json:"status"`
	IncompleteDetails *openAIResponsesIncomplete `json:"incomplete_details"`
	Output            []map[string]any           `json:"output"`
	Usage             openAIResponsesUsage       `json:"usage"`
}

type openAIResponsesIncomplete struct {
	Reason string `json:"reason"`
}

type openAIResponsesUsage struct {
	InputTokens         int                                `json:"input_tokens"`
	OutputTokens        int                                `json:"output_tokens"`
	TotalTokens         int                                `json:"total_tokens"`
	InputTokensDetails  *openAIResponsesUsageInputDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *openAIResponsesUsageOutputDetails `json:"output_tokens_details,omitempty"`
}

type openAIResponsesUsageInputDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type openAIResponsesUsageOutputDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}
