package hcsfmarshal

import (
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func openAIChatToolCall(t *proto.ToolUseNode) map[string]any {
	return map[string]any{
		"id":   t.ToolCallID,
		"type": "function",
		"function": map[string]any{
			"name":      t.Name,
			"arguments": rawJSONString(t.Input),
		},
	}
}

func responseMessage(role string, content []any) map[string]any {
	return map[string]any{"type": "message", "role": role, "content": content}
}

func anthropicResultContent(env *proto.HCSF, blocks []proto.CanonicalContentBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text", "":
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case "image":
			var src map[string]any
			if len(b.Image) > 0 && json.Unmarshal(b.Image, &src) == nil {
				out = append(out, map[string]any{"type": "image", "source": src})
			}
		default:
			addMarshalLossRaw(env, "anthropic_messages", proto.CapabilityToolResult, "", "tool_result content block unsupported by Anthropic projection", "unsupported_tool_result_content")
		}
	}
	return out
}

func flattenContent(blocks []proto.CanonicalContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text", "":
			parts = append(parts, b.Text)
		default:
			if raw, err := json.Marshal(b); err == nil {
				parts = append(parts, string(raw))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func thinkingBlocks(t *proto.ThinkingNode) []proto.CanonicalContentBlock {
	if t == nil || (t.Redaction != "" && t.Redaction != proto.RedactionPublic) {
		return nil
	}
	return t.Blocks
}

func responsesThinkingItems(t *proto.ThinkingNode) []any {
	if t == nil || (t.Redaction != "" && t.Redaction != proto.RedactionPublic && t.Redaction != proto.RedactionProviderOnly) {
		return nil
	}
	providerOnly := t.Redaction == proto.RedactionProviderOnly
	var items []any
	for _, b := range t.Blocks {
		text := firstNonEmpty(b.Text, b.Thinking, b.ReasoningSummary)
		if providerOnly {
			text = ""
		}
		signature := firstNonEmpty(b.Signature, t.Signature)
		if text == "" && signature == "" {
			continue
		}
		summary := []any{}
		if text != "" {
			summary = append(summary, map[string]any{"type": "summary_text", "text": text})
		}
		item := map[string]any{
			"type":    "reasoning",
			"summary": summary,
		}
		if signature != "" {
			item["encrypted_content"] = signature
		}
		items = append(items, item)
	}
	if len(items) == 0 && t.Signature != "" {
		items = append(items, map[string]any{
			"type":              "reasoning",
			"summary":           []any{},
			"encrypted_content": t.Signature,
		})
	}
	return items
}

func cacheTargets(env *proto.HCSF) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, n := range env.CapabilityGraph.Nodes {
		if n.Kind == proto.CapabilityCacheControl && n.CacheControl != nil {
			for _, ref := range n.CacheControl.BreakpointRefs {
				out[ref] = map[string]any{"type": "ephemeral"}
			}
		}
	}
	return out
}

func withCache(block map[string]any, nodeID string, targets map[string]map[string]any, applied map[string]bool) map[string]any {
	if cc, ok := targets[nodeID]; ok {
		block["cache_control"] = cc
		applied[nodeID] = true
	}
	return block
}

func emitUnappliedCacheLoss(env *proto.HCSF, family string, targets map[string]map[string]any, applied map[string]bool) {
	for ref := range targets {
		if !applied[ref] {
			addMarshalLossRaw(env, family, proto.CapabilityCacheControl, ref, "cache_control breakpoint target was not rendered", "cache_target_not_rendered")
		}
	}
}

func anthropicImageBlock(env *proto.HCSF, n proto.CapabilityNode) (map[string]any, bool) {
	if n.Image == nil {
		addMarshalLoss(env, "anthropic_messages", n, "image node missing payload", "missing_image_payload")
		return nil, false
	}
	switch n.Image.SourceKind {
	case proto.DataSourceInlineBase64:
		return map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": n.Image.MediaType, "data": n.Image.Locator.Value}}, true
	case proto.DataSourceURL:
		return map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": n.Image.Locator.Value}}, true
	default:
		addMarshalLoss(env, "anthropic_messages", n, "image source kind unsupported by Anthropic Messages", "unsupported_image_source")
		return nil, false
	}
}

func openAIImagePart(env *proto.HCSF, family string, n proto.CapabilityNode) (map[string]any, bool) {
	if n.Image == nil {
		addMarshalLoss(env, family, n, "image node missing payload", "missing_image_payload")
		return nil, false
	}
	imageURL := n.Image.Locator.Value
	if n.Image.SourceKind == proto.DataSourceInlineBase64 {
		imageURL = "data:" + n.Image.MediaType + ";base64," + n.Image.Locator.Value
	} else if n.Image.SourceKind != proto.DataSourceURL {
		addMarshalLoss(env, family, n, "image source kind unsupported by provider request schema", "unsupported_image_source")
		return nil, false
	}
	if family == "openai_chat" {
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}}, true
	}
	return map[string]any{"type": "input_image", "image_url": imageURL}, true
}

func mergeResponsesNative(env *proto.HCSF, body map[string]any) {
	if env.Extensions == nil {
		return
	}
	raw := env.Extensions["openai_responses:native_body"]
	if len(raw) == 0 {
		return
	}
	var native map[string]any
	if json.Unmarshal(raw, &native) != nil {
		return
	}
	for k, v := range native {
		body[k] = v
	}
}

// InjectRequestControls 将统一请求控制字段投影到已选定的上游协议请求体。
func InjectRequestControls(raw []byte, env *proto.HCSF, family string) ([]byte, error) {
	if family == "dify_chat" {
		// Dify 无 per-request 控制参数(模型/采样在 app 侧配置),openai 形
		// controls 字段一律不可注入;被丢弃的 controls 已在 marshal 内记 loss。
		return raw, nil
	}
	if family == "ollama_native" {
		// Ollama 的采样控制已在 marshal 阶段嵌进 options{}(num_predict 等);
		// 在顶层二次注入 openai 形 temperature/max_tokens 是协议污染。
		// 不可表达的控制已在 marshal 内记 loss。
		return raw, nil
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if family == "gemini_messages" {
		return injectGeminiRequestControls(body, env)
	}
	c := env.RequestControls
	if c.MaxTokens != nil {
		if family == "openai_responses" {
			body["max_output_tokens"] = *c.MaxTokens
		} else {
			body["max_tokens"] = *c.MaxTokens
		}
	}
	if c.Temperature != nil {
		body["temperature"] = *c.Temperature
	}
	if c.TopP != nil {
		body["top_p"] = *c.TopP
	}
	if c.Seed != nil && family == "openai_chat" {
		body["seed"] = *c.Seed
	}
	if c.ParallelToolCalls != nil && family != "anthropic_messages" {
		body["parallel_tool_calls"] = *c.ParallelToolCalls
	}
	if len(c.StopSequences) > 0 && family == "anthropic_messages" {
		body["stop_sequences"] = c.StopSequences
	} else if len(c.Stop) > 0 {
		body["stop"] = c.Stop
	} else if len(c.StopSequences) > 0 {
		body["stop"] = c.StopSequences
	}
	if len(c.ToolChoice) > 0 {
		body["tool_choice"] = rawJSONValue(c.ToolChoice)
	}
	if len(c.Tools) > 0 {
		body["tools"] = renderControlTools(family, c.Tools)
	}
	if c.ResponseFormat != nil {
		// Schema 存 inbound 原始 response_format/text;同协议直通,Chat→Responses 改写为 text.format。
		if c.ResponseFormat.Type == "raw" && len(c.ResponseFormat.Schema) > 0 {
			switch family {
			case "openai_responses":
				if text, ok := OpenAIResponsesTextFromChatResponseFormatRaw(c.ResponseFormat.Schema); ok {
					body["text"] = text
				} else {
					body["text"] = rawJSONValue(c.ResponseFormat.Schema)
				}
			case "openai_chat":
				body["response_format"] = rawJSONValue(c.ResponseFormat.Schema)
			}
		} else {
			rf := map[string]any{"type": c.ResponseFormat.Type}
			if len(c.ResponseFormat.Schema) > 0 {
				rf["schema"] = rawJSONValue(c.ResponseFormat.Schema)
			}
			if c.ResponseFormat.Strict != nil {
				rf["strict"] = *c.ResponseFormat.Strict
			}
			if family == "openai_responses" {
				body["text"] = map[string]any{"format": rf}
			} else if family == "openai_chat" {
				body["response_format"] = rf
			}
		}
	}
	mergeRequestPassthrough(body, env)
	return json.Marshal(body)
}

func OpenAIResponsesTextFromChatResponseFormatRaw(raw json.RawMessage) (map[string]any, bool) {
	var rf map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &rf) != nil {
		return nil, false
	}
	typ, _ := rf["type"].(string)
	format := map[string]any{"type": typ}
	switch typ {
	case "text", "json_object":
	case "json_schema":
		if inner, ok := rf["json_schema"].(map[string]any); ok {
			for _, key := range []string{"name", "strict", "schema"} {
				if v, exists := inner[key]; exists {
					format[key] = v
				}
			}
		}
	default:
		return nil, false
	}
	return map[string]any{"format": format}, true
}

func mergeRequestPassthrough(body map[string]any, env *proto.HCSF) {
	if env == nil || env.Passthrough == nil || len(env.Passthrough.Extra) == 0 {
		return
	}
	for key, raw := range env.Passthrough.Extra {
		if _, exists := body[key]; exists {
			continue
		}
		body[key] = rawJSONValue(raw)
	}
}
func injectGeminiRequestControls(body map[string]any, env *proto.HCSF) ([]byte, error) {
	c := env.RequestControls
	generation := map[string]any{}
	if raw := c.NativeOptions["gemini_messages"]; len(raw) > 0 {
		var native map[string]any
		if json.Unmarshal(raw, &native) == nil {
			for key, value := range native {
				generation[key] = value
			}
		}
	}
	if existing, ok := body["generationConfig"].(map[string]any); ok {
		for k, v := range existing {
			generation[k] = v
		}
	}
	if c.MaxTokens != nil {
		generation["maxOutputTokens"] = *c.MaxTokens
	}
	if c.Temperature != nil {
		generation["temperature"] = *c.Temperature
	}
	if c.TopP != nil {
		generation["topP"] = *c.TopP
	}
	if len(c.StopSequences) > 0 {
		generation["stopSequences"] = c.StopSequences
	} else if len(c.Stop) > 0 {
		generation["stopSequences"] = c.Stop
	}
	if c.ResponseFormat != nil && len(c.ResponseFormat.Schema) > 0 {
		var raw map[string]any
		if json.Unmarshal(c.ResponseFormat.Schema, &raw) == nil {
			// inbound 已是 Gemini 形:responseMimeType/responseSchema 直传(保持原行为)。
			if v, ok := raw["responseMimeType"]; ok && v != "" {
				generation["responseMimeType"] = v
			}
			if v, ok := raw["responseSchema"]; ok {
				generation["responseSchema"] = v
			}
			// inbound 是 OpenAI Chat response_format(json_object / json_schema):翻译成 Gemini 等价物。
			// 此前无此映射 → OpenAI 客户端的结构化输出请求打到 Gemini 上游被静默丢弃(structured_output
			// 能力缝)。Gemini 契约 responseMimeType="application/json" + responseSchema=<schema> 为公开 API。
			if _, has := generation["responseMimeType"]; !has {
				if mime, schema := openAIResponseFormatToGemini(raw); mime != "" {
					generation["responseMimeType"] = mime
					if schema != nil {
						if _, hasSchema := generation["responseSchema"]; !hasSchema {
							generation["responseSchema"] = schema
						}
					}
				}
			}
		}
	}
	if len(generation) > 0 {
		body["generationConfig"] = generation
	}
	if len(c.Tools) > 0 {
		body["tools"] = renderGeminiControlTools(c.Tools)
	}
	mergeRequestPassthrough(body, env)
	return json.Marshal(body)
}

// openAIResponseFormatToGemini 把 inbound 的 OpenAI Chat response_format(已 unmarshal 的 raw map)
// 翻译为 Gemini generationConfig 的 (responseMimeType, responseSchema):
//   - {"type":"json_object"}                                  → ("application/json", nil)    JSON 模式无 schema
//   - {"type":"json_schema","json_schema":{"schema":{...}}}   → ("application/json", <schema>)
//   - 其它(无可识别 type / 已是 Gemini 形)                    → ("", nil)  表示不由本函数映射
//
// 注:Gemini responseSchema 仅接受 OpenAPI 子集,不兼容的 schema 会被上游 4xx 拒(显式失败,优于
// 此前对客户端显式结构化输出请求的静默丢弃)。schema 透传为 any(已是解析后的 JSON 值),由
// json.Marshal 重新序列化进 generationConfig。OpenAI 的 json_schema.name 是元数据、strict 在 Gemini
// 不支持(无对应字段),故二者均不映射(非信息丢失)。json_schema 缺/空 schema 时只回 mime(schema=nil),
// 退化为 Gemini "JSON 模式"(responseMimeType 单独合法、不强制 schema),不注入坏 responseSchema。
func openAIResponseFormatToGemini(raw map[string]any) (mime string, schema any) {
	t, _ := raw["type"].(string)
	switch t {
	case "json_object":
		return "application/json", nil
	case "json_schema":
		if js, ok := raw["json_schema"].(map[string]any); ok {
			if s, ok := js["schema"]; ok {
				return "application/json", s
			}
		}
		return "application/json", nil
	}
	return "", nil
}

func renderGeminiControlTools(tools []proto.CanonicalTool) []any {
	decls := make([]any, 0, len(tools))
	for _, t := range tools {
		decl := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  rawJSONValue(t.InputSchema),
		}
		decls = append(decls, decl)
	}
	if len(decls) == 0 {
		return nil
	}
	return []any{map[string]any{"functionDeclarations": decls}}
}

func renderControlTools(family string, tools []proto.CanonicalTool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		schema := rawJSONValue(t.InputSchema)
		switch family {
		case "openai_chat":
			out = append(out, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": schema}})
		case "openai_responses":
			out = append(out, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": schema})
		default:
			out = append(out, map[string]any{"name": t.Name, "description": t.Description, "input_schema": schema})
		}
	}
	return out
}
