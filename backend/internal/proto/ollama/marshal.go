package ollama

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// marshalVendor 是 ProtocolLossEntry.Vendor 的统一标识。
const marshalVendor = "ollama_native"

// MarshalChatRequest 把 HCSF envelope 投影成 Ollama /api/chat 请求 body。
//
// 关键不变量：
//   - 采样控制只进 options{}（num_predict=MaxTokens 改名），顶层绝不出现
//     temperature/max_tokens 等 openai 形字段。
//   - stream 按 env.StreamPlan.Mode 显式写：Ollama 默认 stream=true，
//     非流式必须显式 false。
//   - 任何不可表达的 capability / control 在 ProtocolLoss 记账，禁止静默丢弃。
func MarshalChatRequest(env *proto.HCSF) ([]byte, error) {
	if env == nil {
		return nil, errors.New("ollama: nil HCSF envelope")
	}

	var messages []chatMessage

	// system 文本的真相源二选一：入站 adapter 会把同一段 system 同时写进
	// RequestControls.SystemPrompt 和一个 role=system 文本节点（节点序常在
	// 对话尾）。SystemPrompt 非空时它是唯一真相源，作为首条 system 消息，
	// 所有 system 节点跳过；为空时才按节点序折叠 system 消息。
	systemFromControls := env.RequestControls.SystemPrompt != ""
	if systemFromControls {
		messages = append(messages, chatMessage{Role: "system", Content: env.RequestControls.SystemPrompt})
	}

	var thinkRequested bool
	for _, n := range env.CapabilityGraph.Nodes {
		switch n.Kind {
		case proto.CapabilityText:
			if n.Text == nil {
				addNodeLoss(env, n, "text node missing payload", "missing_text_payload")
				continue
			}
			if systemFromControls && n.Text.Role == "system" {
				continue
			}
			messages = append(messages, chatMessage{Role: ollamaRole(n.Text.Role), Content: n.Text.Block.Text})
		case proto.CapabilityToolUse:
			if n.ToolUse == nil {
				addNodeLoss(env, n, "tool_use node missing payload", "missing_tool_use_payload")
				continue
			}
			messages = append(messages, chatMessage{
				Role: "assistant",
				ToolCalls: []toolCall{{Function: toolCallFunction{
					Name:      n.ToolUse.Name,
					Arguments: normalizeArgumentsObject(n.ToolUse.Input),
				}}},
			})
		case proto.CapabilityToolResult:
			if n.ToolResult == nil {
				addNodeLoss(env, n, "tool_result node missing payload", "missing_tool_result_payload")
				continue
			}
			messages = append(messages, chatMessage{Role: "tool", Content: flattenBlocks(n.ToolResult.Content)})
		case proto.CapabilityImage:
			appendImage(env, n, &messages)
		case proto.CapabilityThinking:
			if n.Thinking == nil {
				addNodeLoss(env, n, "thinking node missing payload", "missing_thinking_payload")
				continue
			}
			// Ollama /api/chat 用顶层 "think": true 开关思维链;canonical thinking
			// 节点(enabled/adaptive)投影为该开关。budget 与历史 thinking 块在
			// Ollama 契约上不可表达,分别记账,禁止静默蒸发。
			thinkRequested = true
			if n.Thinking.BudgetTokens > 0 {
				addNodeLoss(env, n, "thinking budget_tokens has no ollama_native projection (think flag only)", "thinking_budget_unprojected")
			}
			if len(n.Thinking.Blocks) > 0 {
				addNodeLoss(env, n, "prior thinking blocks have no ollama_native request projection", "thinking_blocks_unprojected")
			}
		default:
			addNodeLoss(env, n, "capability unsupported by ollama_native marshal", "unsupported_capability")
		}
	}

	var think *bool
	if thinkRequested {
		v := true
		think = &v
	}
	body := chatRequest{
		Model:    marshalModel(env),
		Messages: messages,
		Stream:   env.StreamPlan.Mode == proto.StreamModeStreaming,
		Options:  optionsFromControls(env),
		Tools:    requestTools(env.RequestControls.Tools),
		Format:   formatFromControls(env),
		Think:    think,
	}
	recordControlLosses(env)
	return json.Marshal(body)
}

// marshalModel 取 registry 解析后的上游模型名，回落入口模型名。
func marshalModel(env *proto.HCSF) string {
	if env.RequestMeta.UpstreamModel != "" {
		return env.RequestMeta.UpstreamModel
	}
	return env.RequestMeta.Model
}

// ollamaRole 归一 canonical role；未知 role 按 user 兜底。
func ollamaRole(role string) string {
	switch role {
	case "system", "assistant", "tool":
		return role
	default:
		return "user"
	}
}

// appendImage 把 image 节点投影到 messages：base64 追加到上一条 user 消息的
// images[]（无上一条 user 消息时新建空文本 user 消息承载）；URL 形态 Ollama
// 不可表达，记 LOSSY 丢弃。
func appendImage(env *proto.HCSF, n proto.CapabilityNode, messages *[]chatMessage) {
	if n.Image == nil {
		addNodeLoss(env, n, "image node missing payload", "missing_image_payload")
		return
	}
	switch n.Image.SourceKind {
	case proto.DataSourceInlineBase64:
		if n.Image.Locator.Value == "" {
			addNodeLoss(env, n, "image base64 locator empty", "missing_image_data")
			return
		}
		if last := len(*messages) - 1; last >= 0 && (*messages)[last].Role == "user" {
			(*messages)[last].Images = append((*messages)[last].Images, n.Image.Locator.Value)
			return
		}
		*messages = append(*messages, chatMessage{Role: "user", Images: []string{n.Image.Locator.Value}})
	case proto.DataSourceURL:
		addNodeLoss(env, n, "remote URL image cannot be projected; ollama_native images only accept inline base64", "unsupported_image_source")
	default:
		addNodeLoss(env, n, "image source kind unsupported by ollama_native images projection", "unsupported_image_source")
	}
}

// optionsFromControls 把采样控制嵌进 options{}。这是 Ollama 与 openai 形的
// 核心差异：顶层无这些字段，MaxTokens 改名 num_predict。
func optionsFromControls(env *proto.HCSF) map[string]any {
	c := env.RequestControls
	options := map[string]any{}
	if c.MaxTokens != nil {
		options["num_predict"] = *c.MaxTokens
	}
	if c.Temperature != nil {
		options["temperature"] = *c.Temperature
	}
	if c.TopP != nil {
		options["top_p"] = *c.TopP
	}
	if c.Seed != nil {
		options["seed"] = *c.Seed
	}
	if len(c.Stop) > 0 {
		options["stop"] = c.Stop
	} else if len(c.StopSequences) > 0 {
		options["stop"] = c.StopSequences
	}
	if len(options) == 0 {
		return nil
	}
	return options
}

// requestTools 把 canonical tool 声明投影为顶层 tools（OpenAI 形 function 声明）。
func requestTools(tools []proto.CanonicalTool) []requestTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]requestTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, requestTool{
			Type: "function",
			Function: requestToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return out
}

// formatFromControls 投影 ResponseFormat → format 字段。
//
// 真实管线里 ResponseFormat 只有两种 dialect(入站 adapter 写入):
//   - openai 入站:Type="raw",Schema=客户端原始 response_format/text 对象
//     ({"type":"json_object"} / {"type":"json_schema","json_schema":{...}})
//   - gemini 入站:Type="gemini_generation_config",Schema=
//     {responseMimeType, responseSchema}
//
// 这两种都必须解出来投到 Ollama 的 format("json" 字符串或 schema 对象),
// 不许整体记 loss 丢弃——结构化输出在 Ollama 契约上完全可表达,丢弃=客户端
// 要 JSON 拿到散文且无任何可见信号。只有真不可表达的 dialect 才记 loss。
func formatFromControls(env *proto.HCSF) json.RawMessage {
	rf := env.RequestControls.ResponseFormat
	if rf == nil {
		return nil
	}
	switch rf.Type {
	case "json_object", "json":
		return json.RawMessage(`"json"`)
	case "json_schema":
		if len(rf.Schema) > 0 {
			return rf.Schema
		}
		return json.RawMessage(`"json"`)
	case "text", "":
		return nil
	case "raw":
		return formatFromRawDialect(env, rf.Schema)
	case "gemini_generation_config":
		return formatFromGeminiDialect(env, rf.Schema)
	default:
		addControlLoss(env, "response_format", "response_format type has no ollama_native format projection")
		return nil
	}
}

// formatFromRawDialect 解 openai 形 raw 对象(response_format 或 Responses
// 的 text 包装)。
func formatFromRawDialect(env *proto.HCSF, raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		addControlLoss(env, "response_format", "raw response_format payload is not a JSON object")
		return nil
	}
	// Responses 形 {"format":{...}} 包一层,剥开取内层。
	if inner, ok := obj["format"]; ok {
		var innerObj map[string]json.RawMessage
		if json.Unmarshal(inner, &innerObj) == nil {
			obj = innerObj
		}
	}
	var typ string
	if t, ok := obj["type"]; ok {
		_ = json.Unmarshal(t, &typ)
	}
	switch typ {
	case "json_object":
		return json.RawMessage(`"json"`)
	case "json_schema":
		if js, ok := obj["json_schema"]; ok {
			var jsObj map[string]json.RawMessage
			if json.Unmarshal(js, &jsObj) == nil {
				if schema, ok := jsObj["schema"]; ok && len(schema) > 0 {
					return schema
				}
			}
		}
		// schema 缺失时退化为 JSON mode(仍约束输出为合法 JSON)。
		return json.RawMessage(`"json"`)
	case "text", "":
		return nil
	default:
		addControlLoss(env, "response_format", "raw response_format type has no ollama_native format projection")
		return nil
	}
}

// formatFromGeminiDialect 解 gemini generationConfig 形
// {responseMimeType, responseSchema}。
func formatFromGeminiDialect(env *proto.HCSF, raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		addControlLoss(env, "response_format", "gemini generation config payload is not a JSON object")
		return nil
	}
	if schema, ok := obj["responseSchema"]; ok && len(schema) > 0 {
		return schema
	}
	var mime string
	if m, ok := obj["responseMimeType"]; ok {
		_ = json.Unmarshal(m, &mime)
	}
	switch mime {
	case "application/json":
		return json.RawMessage(`"json"`)
	case "", "text/plain":
		return nil
	default:
		addControlLoss(env, "response_format", "gemini responseMimeType has no ollama_native format projection")
		return nil
	}
}

// recordControlLosses 为 Ollama 不存在对应字段的 per-request 控制项记账。
// 可表达的控制（max_tokens/temperature/top_p/seed/stop/tools/response_format
// 的 json 形）已在上面投影，不在此重复记账。
func recordControlLosses(env *proto.HCSF) {
	c := env.RequestControls
	if len(c.ToolChoice) > 0 {
		addControlLoss(env, "tool_choice", "request control has no ollama_native projection")
	}
	if c.ParallelToolCalls != nil {
		addControlLoss(env, "parallel_tool_calls", "request control has no ollama_native projection")
	}
}

// flattenBlocks 把 tool_result 内容块折叠成单字符串（Ollama tool 消息只有
// content 文本载体）。非文本块序列化为 JSON 保留信息。
func flattenBlocks(blocks []proto.CanonicalContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blocks))
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

// normalizeArgumentsObject 保证 tool_calls.function.arguments 是 JSON 对象：
// 空/null 输入归一为 {}（Ollama 的 arguments 是对象，不接受 null/字符串）。
func normalizeArgumentsObject(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{}`)
	}
	out := make([]byte, len(trimmed))
	copy(out, trimmed)
	return out
}

// addNodeLoss 把 capability 节点级 marshal loss 追加到 envelope 图。
func addNodeLoss(env *proto.HCSF, n proto.CapabilityNode, reason, code string) {
	loss, _ := proto.NewClientLossEntry(proto.ProtocolLossWarning, reason, code, n.Kind, n.ID)
	loss.Direction = string(proto.DirectionCanonicalToUpstream)
	loss.Vendor = marshalVendor
	env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, loss)
}

// addControlLoss 把 request control 级 marshal loss 追加到 envelope 图。
func addControlLoss(env *proto.HCSF, field, reason string) {
	loss, _ := proto.NewClientLossEntry(proto.ProtocolLossWarning, reason, "unsupported_request_control", "", "")
	loss.Direction = string(proto.DirectionCanonicalToUpstream)
	loss.Vendor = marshalVendor
	loss.Field = field
	env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, loss)
}
