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
		// detail=low/high/auto 控制上游 vision 计费档;当前建模路径不承载它,
		// 丢弃时记 info loss 保住「丢必记」可观测性(不影响图本身)。
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rawImageURL, &shape); err != nil {
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_missing_url", "invalid_image_url", CapabilityImage, "")
		return nil, []ProtocolLossEntry{loss}
	}
	return imageNodeFromURL(shape.URL, shape.Detail)
}

// imageNodeFromURL 把 image URL 字符串(data URI 或 http url)+ detail 提示转 ImageNode,
// 供 openai_chat 的 image_url 对象与 openai_responses 的 input_image 字符串共用同一套
// data URI / detail / 大小写判定内核(两处不漂移)。空/畸形 → nil + warning loss,调用方跳过。
func imageNodeFromURL(imageURL, detail string) (*ImageNode, []ProtocolLossEntry) {
	if imageURL == "" {
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_missing_url", "invalid_image_url", CapabilityImage, "")
		return nil, []ProtocolLossEntry{loss}
	}
	var losses []ProtocolLossEntry
	// detail 非空且非默认 auto 时,记 info loss(low/high 精度提示未投射到上游)。
	if d := strings.ToLower(strings.TrimSpace(detail)); d != "" && d != "auto" {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_image_detail_dropped:"+d, "image_detail_dropped", CapabilityImage, "")
		losses = append(losses, loss)
	}
	// data URI 前缀判定大小写不敏感(RFC2397 scheme 不区分大小写)。
	if len(imageURL) >= 5 && strings.EqualFold(imageURL[:5], "data:") {
		mediaType, data, ok := parseBase64DataURI(imageURL)
		if !ok {
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_malformed_data_uri", "invalid_image_url", CapabilityImage, "")
			return nil, append(losses, loss)
		}
		return &ImageNode{
			SourceKind: DataSourceInlineBase64,
			MediaType:  mediaType,
			Locator:    DataLocator{Kind: DataSourceInlineBase64, Value: data},
		}, losses
	}
	return &ImageNode{
		SourceKind: DataSourceURL,
		// OpenAI 形态 URL 不带独立 mime 字段,留空由上游按 URL 自判。
		Locator: DataLocator{Kind: DataSourceURL, Value: imageURL},
	}, losses
}

// parseBase64DataURI 解 data:<mime>;base64,<data>;mime 与 data 皆非空才算合法。
// mediaType 归一为「第一个 ';' 前的主类型」并转小写——data URI 的 mime 段可带
// charset 等参数(如 image/png;charset=utf-8),这些参数对图像识别无意义且会让
// 跨族渲染时上游按非法 mime 4xx 拒;前缀 "data:" 已由调用方大小写不敏感判定。
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
