// anthropic_request_translator.go — Bedrock 闭环最后一块：把 Anthropic
// Messages API body 翻译成 AWS Bedrock invoke body。
//
// 调用链:
//
//	Anthropic CLI / Claude Code  ─→  HUAKAI /v1/messages handler 处理器
//	─→  TranslateAnthropicAPIToBedrock(body)
//	─→  PassthroughAdapter (sigv4 sign)
//	─→  AWS Bedrock invoke-with-response-stream 端点
//	─→  binary EventStream 二进制事件流
//	─→  BedrockEventStreamScanner (gateway A3)
//	─→  BedrockEventStreamAdapter (proto A4)
//	─→  forwarder (SSE 输出)
//	─→  Anthropic CLI（透传 Anthropic 事件，无需 client 端再翻译）
//
// 这是 OpenAI client → Bedrock 闭环的"低成本预演"——Anthropic CLI 已经
// 期望 Anthropic Messages 形态，所以返回端不用 ClientAdapter 翻译。
// OpenAI client → Bedrock 闭环（需 ClientAdapter）放更后面。
//
// 翻译规则（参 https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages.html
// 公开 schema, 不读 aws-sdk-go 源）:
//
//	Anthropic API 原 body (示例):
//	  {"model":"claude-3-5-sonnet-20241022", "messages":[...],
//	   "max_tokens":1024, "stream":true, "system":"...", ...}
//
//	Bedrock invoke body (目标):
//	  {"anthropic_version":"bedrock-2023-05-31", "messages":[...],
//	   "max_tokens":1024, "system":"...", ...}
//
// 关键差异:
//   - **strip "model"**: 剥离 "model"；Bedrock URL 中已含 model_id，body 不能再有
//   - **strip "stream"**: 剥离 "stream"；Bedrock 用 endpoint URL 切流式（invoke vs
//     invoke-with-response-stream）
//   - **inject "anthropic_version": "bedrock-2023-05-31"**: AWS 要求字段
//   - 其它字段（messages / max_tokens / system / temperature / tools 等）
//     直接透传
//   - 未识别字段（vendor 添加新字段如 "service_tier"）直接透传——保 U7
//     字段透传一致性
package bedrock

import (
	"encoding/json"
	"errors"
	"fmt"
)

// AnthropicVersionBedrock 是 Bedrock 要求的 anthropic_version 字段值。
// 参 AWS 公开文档 https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages.html
const AnthropicVersionBedrock = "bedrock-2023-05-31"

// IsAnthropicMessagesShape 判断 body 是否看起来像 Anthropic Messages API
// 形态。判据：顶层 JSON object 含 "messages" 字段（array 形）。
//
// 用途：bedrock_invoke 协议族同时承载
// Anthropic / Cohere / Llama / Mistral / Titan 等多家 vendor，AutoTranslate
// 不能盲目对所有 body 跑。Cohere/Llama/Titan body 形态分别用 prompt /
// inputText / message 等字段，没有 messages array — 这个 helper 让 caller
// 先确认是 Anthropic 形态再翻译。
//
// 行为：
//   - 空 body / 非 JSON object → false
//   - JSON object 但没有 "messages" 字段 → false
//   - JSON object 有 "messages" 字段（任意值，包括 null/空数组）→ true
//
// 注：本 helper 是粗粒度形态检测，不严格 schema 校验；后续翻译流程仍会
// 处理边界（如 messages: null 时 Bedrock 会拒绝）。
func IsAnthropicMessagesShape(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || raw == nil {
		return false
	}
	_, has := raw["messages"]
	return has
}

// ErrEmptyAnthropicBody 表示输入 body 为空。
var ErrEmptyAnthropicBody = errors.New("bedrock: Anthropic API body 为空")

// ErrInvalidAnthropicJSON 表示输入 body 不是合法 JSON object。
var ErrInvalidAnthropicJSON = errors.New("bedrock: Anthropic API body 不是合法 JSON object")

// AnthropicAPIToBedrockResult 携带翻译结果 + 是否流式（用于 endpoint 选择）。
type AnthropicAPIToBedrockResult struct {
	// Body: Bedrock 期望形态的 JSON bytes（已合并 anthropic_version）
	Body []byte

	// Stream: 原 body 是否含 stream:true。caller 据此选 invoke 或
	// invoke-with-response-stream endpoint 端点。
	Stream bool

	// UpstreamModelID: 原 body 的 model 字段（被剥离后保留供 caller 用作
	// URL path）。如果 body 没声明 model，返回空字符串（caller 应另行
	// 决定 model_id 来源）。
	UpstreamModelID string
}

// TranslateAnthropicAPIToBedrock 把 Anthropic Messages API 的 body 翻译成
// AWS Bedrock invoke 期望形态。
//
// 行为:
//  1. 输入必须是 JSON object（顶层非 object 返回 ErrInvalidAnthropicJSON）
//  2. 抽取 model + stream 到 result（不再放进 Body）
//  3. 注入 anthropic_version (如已存在则沿用——尊重 caller 显式覆盖)
//  4. 其它字段全保留（U7 字段透传一致语义）
//
// 不处理:
//   - 不做 model alias 映射（caller 应预先把 anthropic API 形 model 映射
//     成 bedrock model_id 如 anthropic.claude-3-5-sonnet-20241022-v2:0）
//   - 不动 messages / system / tools 内部结构（Anthropic API 与 Bedrock
//     在这些字段语义一致）
func TranslateAnthropicAPIToBedrock(anthropicBody []byte) (AnthropicAPIToBedrockResult, error) {
	if len(anthropicBody) == 0 {
		return AnthropicAPIToBedrockResult{}, ErrEmptyAnthropicBody
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(anthropicBody, &raw); err != nil {
		return AnthropicAPIToBedrockResult{}, fmt.Errorf("%w: %v", ErrInvalidAnthropicJSON, err)
	}
	// json.Unmarshal("null", &raw) 不报错但 raw 仍为 nil；后续 map assign
	// 会 panic。提前 fail-fast.
	if raw == nil {
		return AnthropicAPIToBedrockResult{}, fmt.Errorf("%w: 顶层为 null 或非 object", ErrInvalidAnthropicJSON)
	}

	// 抽 model（剥离前先记下供 caller 用）
	var modelID string
	if rawModel, ok := raw["model"]; ok {
		_ = json.Unmarshal(rawModel, &modelID) // 失败 → modelID 保持 ""
		delete(raw, "model")
	}

	// 抽 stream
	var stream bool
	if rawStream, ok := raw["stream"]; ok {
		_ = json.Unmarshal(rawStream, &stream)
		delete(raw, "stream")
	}

	// Bedrock 对未知顶层字段返回 400 ValidationException(真实流量验证):
	// 剥除 Anthropic 直连专有顶层字段。metadata 是重灾区
	// ——Claude Code 客户端必带 metadata.user_id,不剥则默认 AutoTranslate 配置下
	// 真实 CC 流量经 Bedrock 必 400,整条 Bedrock CC 闭环坏死。
	delete(raw, "metadata")
	// body 内嵌 anthropic_beta:Bedrock 只支持子集,按白名单过滤;过滤后为空则
	// 整字段删除,防不支持的 beta token 直达 Bedrock 触发 400。
	if rawBetas, ok := raw["anthropic_beta"]; ok {
		if filtered, keep := filterBedrockBetaTokens(rawBetas); keep {
			raw["anthropic_beta"] = filtered
		} else {
			delete(raw, "anthropic_beta")
		}
	}

	// 注入 anthropic_version（如未声明）
	if _, exists := raw["anthropic_version"]; !exists {
		raw["anthropic_version"] = json.RawMessage(`"` + AnthropicVersionBedrock + `"`)
	}

	// 序列化（map[string]json.RawMessage 保留 raw 字节嵌套结构）
	out, err := json.Marshal(raw)
	if err != nil {
		return AnthropicAPIToBedrockResult{}, fmt.Errorf("bedrock: 翻译后 marshal 失败: %w", err)
	}

	return AnthropicAPIToBedrockResult{
		Body:            out,
		Stream:          stream,
		UpstreamModelID: modelID,
	}, nil
}

// bedrockSupportedBetaTokens 是 Bedrock 认识的 anthropic beta token 白名单
// (不在名单的 token Bedrock 会 400)。
var bedrockSupportedBetaTokens = map[string]struct{}{
	"computer-use-2024-10-22":                {},
	"computer-use-2025-01-24":                {},
	"token-efficient-tools-2025-02-19":       {},
	"interleaved-thinking-2025-05-14":        {},
	"fine-grained-tool-streaming-2025-05-14": {},
	"context-1m-2025-08-07":                  {},
	"context-management-2025-06-27":          {},
}

// filterBedrockBetaTokens 解析 body 内嵌 anthropic_beta(数组或单字符串),
// 只保留白名单 token;keep=false 表示该字段应整体删除。
func filterBedrockBetaTokens(raw json.RawMessage) (json.RawMessage, bool) {
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		var single string
		if err2 := json.Unmarshal(raw, &single); err2 != nil {
			return nil, false
		}
		list = []string{single}
	}
	kept := make([]string, 0, len(list))
	for _, t := range list {
		if _, ok := bedrockSupportedBetaTokens[t]; ok {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return nil, false
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return nil, false
	}
	return out, true
}
