package proto

import (
	"encoding/json"
	"fmt"
)

// OpenAI Responses 的内部请求与响应形状，以及入站解析辅助。
type openAIResponsesRequest struct {
	Model                string            `json:"model"`
	Input                json.RawMessage   `json:"input"`
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
	ID               string                         `json:"id,omitempty"`
	Status           string                         `json:"status,omitempty"`
	EncryptedContent string                         `json:"encrypted_content,omitempty"`
	Summary          []openAIResponsesReasoningPart `json:"summary,omitempty"`
	Role             string                         `json:"role,omitempty"`
	Content          json.RawMessage                `json:"content,omitempty"`
	CallID           string                         `json:"call_id,omitempty"`
	Name             string                         `json:"name,omitempty"`
	Arguments        string                         `json:"arguments,omitempty"`
	Output           string                         `json:"output,omitempty"`
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

func parseOpenAIResponsesInput(raw json.RawMessage) ([]openAIResponsesInputItem, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		contentBytes, _ := json.Marshal([]openAIResponsesInputPart{{Type: "input_text", Text: s}})
		return []openAIResponsesInputItem{{Type: "message", Role: "user", Content: contentBytes}}, nil
	}
	var items []openAIResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("proto: openai_responses 'input' must be string or array of items: %w", err)
	}
	return items, nil
}

func parseOpenAIResponsesContent(raw json.RawMessage, mi int) ([]openAIChatParsedPart, []ProtocolLossEntry, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []openAIChatParsedPart{{Kind: "text", Text: s}}, nil, nil
	}
	var parts []openAIResponsesInputPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, nil, fmt.Errorf("proto: openai_responses input[%d].content must be string or part array", mi)
	}
	var out []openAIChatParsedPart
	var losses []ProtocolLossEntry
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text":
			out = append(out, openAIChatParsedPart{Kind: "text", Text: p.Text})
		case "input_image":
			node, imageLosses := buildOpenAIResponsesImageNode(p.ImageURL, p.Detail)
			losses = append(losses, imageLosses...)
			if node != nil {
				out = append(out, openAIChatParsedPart{Kind: "image", Image: node, Raw: append(json.RawMessage(nil), p.ImageURL...)})
			}
		case "input_file":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_input_file_d9_pending", "d9_file_pending", CapabilityFile, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_unknown_part:"+p.Type, "unknown_part_type", "", "")
			losses = append(losses, loss)
		}
	}
	return out, losses, nil
}

// buildOpenAIResponsesImageNode 同时兼容裸字符串和对象形式的 image_url。
func buildOpenAIResponsesImageNode(rawImageURL json.RawMessage, detail string) (*ImageNode, []ProtocolLossEntry) {
	if len(rawImageURL) == 0 {
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_missing_url", "invalid_image_url", CapabilityImage, "")
		return nil, []ProtocolLossEntry{loss}
	}
	var url string
	if err := json.Unmarshal(rawImageURL, &url); err != nil {
		var shape struct {
			URL    string `json:"url"`
			Detail string `json:"detail"`
		}
		if err2 := json.Unmarshal(rawImageURL, &shape); err2 != nil {
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_missing_url", "invalid_image_url", CapabilityImage, "")
			return nil, []ProtocolLossEntry{loss}
		}
		url = shape.URL
		if detail == "" {
			detail = shape.Detail
		}
	}
	return imageNodeFromURL(url, detail)
}

func convertOpenAIResponsesTools(tools []json.RawMessage) ([]CanonicalTool, []ProtocolLossEntry, error) {
	var canonicalTools []CanonicalTool
	var losses []ProtocolLossEntry
	for i, rt := range tools {
		var head struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
			Function    *struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
			} `json:"function,omitempty"`
		}
		if err := json.Unmarshal(rt, &head); err != nil {
			return nil, nil, fmt.Errorf("proto: openai_responses tools[%d] parse: %w", i, err)
		}
		switch head.Type {
		case "function", "":
			name, description, parameters := head.Name, head.Description, head.Parameters
			if head.Function != nil {
				if name == "" {
					name = head.Function.Name
				}
				if description == "" {
					description = head.Function.Description
				}
				if len(parameters) == 0 {
					parameters = head.Function.Parameters
				}
			}
			if name == "" {
				return nil, nil, fmt.Errorf("proto: openai_responses tools[%d] function tool missing name", i)
			}
			canonicalTools = append(canonicalTools, CanonicalTool{Name: name, Description: description, InputSchema: parameters})
		case "web_search", "web_search_preview", "code_interpreter", "computer_use_preview", "file_search":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_builtin_tool_native_required:"+head.Type, "builtin_tool_native_required", CapabilityToolUse, "")
			loss.NativePath = "/v1/native/openai/responses"
			loss.Suggestion = "use native passthrough at /v1/native/openai/responses; Mandatory Roadmap for plugin shell"
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_unknown_tool_type:"+head.Type, "unknown_tool_type", CapabilityToolUse, "")
			losses = append(losses, loss)
		}
	}
	return canonicalTools, losses, nil
}
