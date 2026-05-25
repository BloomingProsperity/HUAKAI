package proto

import (
	"encoding/json"
	"fmt"
)

// parseOpenAIChatContent 解 message.content 字段：string 视为单一 text；array
// 解 content parts。
func parseOpenAIChatContent(raw json.RawMessage, mi int) ([]string, []ProtocolLossEntry, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil, nil
	}
	var parts []openAIChatContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, nil, fmt.Errorf("proto: openai_chat messages[%d].content must be string or part array", mi)
	}
	var texts []string
	var losses []ProtocolLossEntry
	for _, p := range parts {
		switch p.Type {
		case "text":
			texts = append(texts, p.Text)
		case "image_url":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_d5_pending", "d5_image_pending", CapabilityImage, "")
			losses = append(losses, loss)
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
	return texts, losses, nil
}

// convertOpenAITools 把 OpenAI tools[] 转 CanonicalTool[]。
func convertOpenAITools(tools []openAIChatTool) ([]CanonicalTool, error) {
	out := make([]CanonicalTool, 0, len(tools))
	for i, t := range tools {
		if t.Type != "" && t.Type != "function" {
			return nil, fmt.Errorf("proto: openai_chat tools[%d] unsupported type %q (only 'function' in D5)", i, t.Type)
		}
		if t.Function.Name == "" {
			return nil, fmt.Errorf("proto: openai_chat tools[%d] missing function.name", i)
		}
		out = append(out, CanonicalTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return out, nil
}

// parseOpenAIStop 解 stop 字段：string 或 []string。
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

// flattenOpenAIToolResultContent 把 tool message 的 text parts 拼成单串。
func flattenOpenAIToolResultContent(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "\n" + p
	}
	return out
}
