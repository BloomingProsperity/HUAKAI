// anthropic_request_translator.go — Bedrock 闭环最后一块：把 Anthropic
// Messages API body 翻译成 AWS Bedrock invoke body.
//
// 调用链:
//   Anthropic CLI / Claude Code  ─→  HUAKAI /v1/messages handler
//   ─→  TranslateAnthropicAPIToBedrock(body)
//   ─→  PassthroughAdapter (sigv4 sign)
//   ─→  AWS Bedrock invoke-with-response-stream
//   ─→  binary EventStream
//   ─→  BedrockEventStreamScanner (gateway A3)
//   ─→  BedrockEventStreamAdapter (proto A4)
//   ─→  forwarder (SSE 输出)
//   ─→  Anthropic CLI（透传 Anthropic 事件，无需 client 端再翻译）
//
// 这是 OpenAI client → Bedrock 闭环的"低成本预演"——Anthropic CLI 已经
// 期望 Anthropic Messages 形态，所以返回端不用 ClientAdapter 翻译。
// OpenAI client → Bedrock 闭环（需 ClientAdapter）放更后面。
//
// 翻译规则（参 https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages.html
// 公开 schema, 不读 aws-sdk-go 源）:
//
//   Anthropic API 原 body (示例):
//     {"model":"claude-3-5-sonnet-20241022", "messages":[...],
//      "max_tokens":1024, "stream":true, "system":"...", ...}
//
//   Bedrock invoke body (目标):
//     {"anthropic_version":"bedrock-2023-05-31", "messages":[...],
//      "max_tokens":1024, "system":"...", ...}
//
// 关键差异:
//   - **strip "model"**: Bedrock URL 中已含 model_id，body 不能再有
//   - **strip "stream"**: Bedrock 用 endpoint URL 切流式（invoke vs
//     invoke-with-response-stream）
//   - **inject "anthropic_version": "bedrock-2023-05-31"**: AWS 要求字段
//   - 其它字段（messages / max_tokens / system / temperature / tools 等）
//     直接 carry-over
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

// ErrEmptyAnthropicBody 表示输入 body 为空。
var ErrEmptyAnthropicBody = errors.New("bedrock: Anthropic API body 为空")

// ErrInvalidAnthropicJSON 表示输入 body 不是合法 JSON object。
var ErrInvalidAnthropicJSON = errors.New("bedrock: Anthropic API body 不是合法 JSON object")

// AnthropicAPIToBedrockResult 携带翻译结果 + 是否流式（用于 endpoint 选择）。
type AnthropicAPIToBedrockResult struct {
	// Body: Bedrock 期望形态的 JSON bytes（已合并 anthropic_version）
	Body []byte

	// Stream: 原 body 是否含 stream:true。caller 据此选 invoke 或
	// invoke-with-response-stream endpoint。
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
