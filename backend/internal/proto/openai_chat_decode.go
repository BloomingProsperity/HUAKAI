package proto

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OpenAI Chat Completions 的内部请求形状与解析辅助。
type openAIChatRequest struct {
	Model               string           `json:"model"`
	Messages            []openAIChatMsg  `json:"messages"`
	Stream              *bool            `json:"stream"`
	MaxTokens           *int             `json:"max_tokens"`
	MaxCompletionTokens *int             `json:"max_completion_tokens"`
	Temperature         *float64         `json:"temperature"`
	TopP                *float64         `json:"top_p"`
	Stop                json.RawMessage  `json:"stop,omitempty"`
	Tools               []openAIChatTool `json:"tools,omitempty"`
	ToolChoice          json.RawMessage  `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool            `json:"parallel_tool_calls"`
	ResponseFormat      json.RawMessage  `json:"response_format,omitempty"`
	ReasoningEffort     string           `json:"reasoning_effort,omitempty"`
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
	ToolCallID string               `json:"tool_call_id,omitempty"`
}

type openAIChatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
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

type openAIChatResponseFormatShape struct {
	Type       string `json:"type"`
	JSONSchema *struct {
		Schema json.RawMessage `json:"schema,omitempty"`
		Strict *bool           `json:"strict,omitempty"`
	} `json:"json_schema,omitempty"`
}

type openAIChatParsedPart struct {
	Kind  string
	Text  string
	Image *ImageNode
	Raw   json.RawMessage
}

// parseOpenAIChatContent 解 message.content 字段，保留 text/image 的原始交织顺序。
func parseOpenAIChatContent(raw json.RawMessage, mi int) ([]openAIChatParsedPart, []ProtocolLossEntry, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []openAIChatParsedPart{{Kind: "text", Text: s}}, nil, nil
	}
	var parts []openAIChatContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, nil, fmt.Errorf("proto: openai_chat messages[%d].content must be string or part array", mi)
	}
	var out []openAIChatParsedPart
	var losses []ProtocolLossEntry
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, openAIChatParsedPart{Kind: "text", Text: p.Text})
		case "image_url":
			node, imageLosses := buildOpenAIImageNode(p.ImageURL)
			losses = append(losses, imageLosses...)
			if node != nil {
				out = append(out, openAIChatParsedPart{Kind: "image", Image: node, Raw: append(json.RawMessage(nil), p.ImageURL...)})
			}
		case "input_audio":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_input_audio_d5_pending", "d5_audio_pending", CapabilityAudio, "")
			losses = append(losses, loss)
		case "file":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_file_part_d5_pending", "d5_file_part_pending", CapabilityFile, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_unknown_content_part:"+p.Type, "unknown_content_part", CapabilityText, "")
			losses = append(losses, loss)
		}
	}
	return out, losses, nil
}

// buildOpenAIImageNode 把 image_url part 转 ImageNode。
func buildOpenAIImageNode(rawImageURL json.RawMessage) (*ImageNode, []ProtocolLossEntry) {
	var shape struct {
		URL    string `json:"url"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rawImageURL, &shape); err != nil {
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_missing_url", "invalid_image_url", CapabilityImage, "")
		return nil, []ProtocolLossEntry{loss}
	}
	return imageNodeFromURL(shape.URL, shape.Detail)
}

// imageNodeFromURL 供 Chat image_url 与 Responses input_image 共用。
func imageNodeFromURL(imageURL, detail string) (*ImageNode, []ProtocolLossEntry) {
	if imageURL == "" {
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_missing_url", "invalid_image_url", CapabilityImage, "")
		return nil, []ProtocolLossEntry{loss}
	}
	var losses []ProtocolLossEntry
	if d := strings.ToLower(strings.TrimSpace(detail)); d != "" && d != "auto" {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_image_detail_dropped:"+d, "image_detail_dropped", CapabilityImage, "")
		losses = append(losses, loss)
	}
	if len(imageURL) >= 5 && strings.EqualFold(imageURL[:5], "data:") {
		mediaType, data, ok := parseBase64DataURI(imageURL)
		if !ok {
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_malformed_data_uri", "invalid_image_url", CapabilityImage, "")
			return nil, append(losses, loss)
		}
		return &ImageNode{SourceKind: DataSourceInlineBase64, MediaType: mediaType, Locator: DataLocator{Kind: DataSourceInlineBase64, Value: data}}, losses
	}
	return &ImageNode{SourceKind: DataSourceURL, Locator: DataLocator{Kind: DataSourceURL, Value: imageURL}}, losses
}

// parseBase64DataURI 解 data:<mime>;base64,<data> 并规范化 MIME 主类型。
func parseBase64DataURI(uri string) (mediaType, data string, ok bool) {
	rest := uri[len("data:"):]
	mediaType, data, found := strings.Cut(rest, ";base64,")
	if !found || mediaType == "" || data == "" {
		return "", "", false
	}
	mainType, _, _ := strings.Cut(mediaType, ";")
	mainType = strings.ToLower(strings.TrimSpace(mainType))
	if mainType == "" {
		return "", "", false
	}
	return mainType, data, true
}

func convertOpenAITools(tools []openAIChatTool) ([]CanonicalTool, error) {
	out := make([]CanonicalTool, 0, len(tools))
	for i, t := range tools {
		if t.Type != "" && t.Type != "function" {
			return nil, fmt.Errorf("proto: openai_chat tools[%d] unsupported type %q (only 'function' in D5)", i, t.Type)
		}
		if t.Function.Name == "" {
			return nil, fmt.Errorf("proto: openai_chat tools[%d] missing function.name", i)
		}
		out = append(out, CanonicalTool{Name: t.Function.Name, Description: t.Function.Description, InputSchema: t.Function.Parameters})
	}
	return out, nil
}

func parseOpenAIStop(raw json.RawMessage) ([]string, error) {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("proto: openai_chat 'stop' must be string or array of strings: %w", err)
	}
	return arr, nil
}

func flattenOpenAIToolResultContent(parts []openAIChatParsedPart) (string, []ProtocolLossEntry) {
	var texts []string
	var losses []ProtocolLossEntry
	for _, p := range parts {
		switch p.Kind {
		case "text":
			texts = append(texts, p.Text)
		case "image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_d5_pending", "d5_image_pending", CapabilityImage, "")
			losses = append(losses, loss)
		}
	}
	return strings.Join(texts, "\n"), losses
}
