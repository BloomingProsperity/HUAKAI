package proto

import (
	"encoding/json"
	"fmt"
)

func parseOpenAIResponsesInput(raw json.RawMessage) ([]openAIResponsesInputItem, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		// 顶层 input string 视为单一 user message。
		contentBytes, _ := json.Marshal([]openAIResponsesInputPart{{Type: "input_text", Text: s}})
		return []openAIResponsesInputItem{{Type: "message", Role: "user", Content: contentBytes}}, nil
	}
	var items []openAIResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("proto: openai_responses 'input' must be string or array of items: %w", err)
	}
	return items, nil
}

func parseOpenAIResponsesContent(raw json.RawMessage, mi int) ([]string, []ProtocolLossEntry, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil, nil
	}
	var parts []openAIResponsesInputPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, nil, fmt.Errorf("proto: openai_responses input[%d].content must be string or part array", mi)
	}
	var texts []string
	var losses []ProtocolLossEntry
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text":
			texts = append(texts, p.Text)
		case "input_image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_input_image_d9_pending", "d9_image_pending", CapabilityImage, "")
			losses = append(losses, loss)
		case "input_file":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_input_file_d9_pending", "d9_file_pending", CapabilityFile, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_unknown_part:"+p.Type, "unknown_part_type", "", "")
			losses = append(losses, loss)
		}
	}
	return texts, losses, nil
}

// convertOpenAIResponsesTools 把 Responses tools[] 转 CanonicalTool[]。
// function 类型直接转；built-in 类型（web_search / code_interpreter / ...）
// 按 synthesis Q9 决策 D：emit native_required loss + Mandatory Roadmap，不入
// RequestControls.Tools。
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
			// 新 Responses 形态：name/description/parameters 直接顶层；旧形态 function: {...}。
			name := head.Name
			desc := head.Description
			params := head.Parameters
			if head.Function != nil {
				if name == "" {
					name = head.Function.Name
				}
				if desc == "" {
					desc = head.Function.Description
				}
				if len(params) == 0 {
					params = head.Function.Parameters
				}
			}
			if name == "" {
				return nil, nil, fmt.Errorf("proto: openai_responses tools[%d] function tool missing name", i)
			}
			canonicalTools = append(canonicalTools, CanonicalTool{
				Name:        name,
				Description: desc,
				InputSchema: params,
			})
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
