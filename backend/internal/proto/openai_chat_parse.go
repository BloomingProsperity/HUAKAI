package proto

import (
	"encoding/json"
	"fmt"
	"strings"
)

// openAIChatParsedPart 是 parseOpenAIChatContent 的有序输出单元,保持
// text/image 交织顺序,供调用方逐块建 canonical block + capability 节点。
type openAIChatParsedPart struct {
	Kind  string          // "text" | "image"
	Text  string          // Kind=="text" 时的文本
	Image *ImageNode      // Kind=="image" 时已解析的图片载荷
	Raw   json.RawMessage // image_url 原始 JSON,供 block.Image 透传
}

// parseOpenAIChatContent 解 message.content 字段：string 视为单一 text；array
// 解 content parts。image_url 建 ImageNode(镜像 buildAnthropicImageNode);
// 畸形 image_url 记 warning loss 不静默丢也不 hard error。
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
			node, imgLosses := buildOpenAIImageNode(p.ImageURL)
			losses = append(losses, imgLosses...)
			if node == nil {
				continue
			}
			out = append(out, openAIChatParsedPart{
				Kind:  "image",
				Image: node,
				Raw:   append(json.RawMessage(nil), p.ImageURL...),
			})
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

// buildOpenAIImageNode 把 image_url part 转 ImageNode。data URI 前缀
// (data:<mime>;base64,<data>)→ inline_base64 且 Locator 只存 base64 本体
// (渲染侧 openAIImagePart 会重组前缀);其余非空 url → url kind 透传。
// url 缺失/畸形 data URI → 返回 nil + warning loss,调用方跳过该 part。
func buildOpenAIImageNode(rawImageURL json.RawMessage) (*ImageNode, []ProtocolLossEntry) {
	var shape struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rawImageURL, &shape); err != nil || shape.URL == "" {
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_missing_url", "invalid_image_url", CapabilityImage, "")
		return nil, []ProtocolLossEntry{loss}
	}
	if strings.HasPrefix(shape.URL, "data:") {
		mediaType, data, ok := parseBase64DataURI(shape.URL)
		if !ok {
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_malformed_data_uri", "invalid_image_url", CapabilityImage, "")
			return nil, []ProtocolLossEntry{loss}
		}
		return &ImageNode{
			SourceKind: DataSourceInlineBase64,
			MediaType:  mediaType,
			Locator:    DataLocator{Kind: DataSourceInlineBase64, Value: data},
		}, nil
	}
	return &ImageNode{
		SourceKind: DataSourceURL,
		// OpenAI 形态 URL 不带独立 mime 字段,留空由上游按 URL 自判。
		Locator: DataLocator{Kind: DataSourceURL, Value: shape.URL},
	}, nil
}

// parseBase64DataURI 解 data:<mime>;base64,<data>;mime 与 data 皆非空才算合法。
func parseBase64DataURI(uri string) (mediaType, data string, ok bool) {
	rest := strings.TrimPrefix(uri, "data:")
	mediaType, data, found := strings.Cut(rest, ";base64,")
	if !found || mediaType == "" || data == "" {
		return "", "", false
	}
	return mediaType, data, true
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

// flattenOpenAIToolResultContent 把 tool message 的 text parts 拼成单串;
// tool result 带图沿用 deferred 策略(渲染侧 tool_result 只吃 text),记原
// d5 pending loss 不静默丢。
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
