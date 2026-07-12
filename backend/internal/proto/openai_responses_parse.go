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
			// F4 视觉:Responses input_image 建 CapabilityImage 节点(镜像 openai_chat 的 image_url 修复)。
			// 此前只记 d9_image_pending loss 把图丢了,HCSF 默认开时上游收不到图。
			node, imgLosses := buildOpenAIResponsesImageNode(p.ImageURL, p.Detail)
			losses = append(losses, imgLosses...)
			if node == nil {
				continue // 畸形/缺失已记 loss,跳过该 part
			}
			out = append(out, openAIChatParsedPart{
				Kind:  "image",
				Image: node,
				Raw:   append(json.RawMessage(nil), p.ImageURL...),
			})
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

// buildOpenAIResponsesImageNode 把 Responses input_image 的 image_url + 部件级 detail 转 ImageNode。
// Responses 规范里 image_url 是裸字符串(data URI 或 http url)、detail 是 part 的兄弟字段;
// 兼容个别 SDK 传 {url,detail} 对象。判定内核复用 imageNodeFromURL(与 openai_chat 同一套)。
func buildOpenAIResponsesImageNode(rawImageURL json.RawMessage, detail string) (*ImageNode, []ProtocolLossEntry) {
	if len(rawImageURL) == 0 {
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_missing_url", "invalid_image_url", CapabilityImage, "")
		return nil, []ProtocolLossEntry{loss}
	}
	var url string
	if err := json.Unmarshal(rawImageURL, &url); err != nil {
		// 兼容对象形态 {url, detail}(part 级 detail 优先,缺省再取对象内 detail)。
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
