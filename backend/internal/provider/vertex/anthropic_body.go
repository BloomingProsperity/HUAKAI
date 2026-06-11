// anthropic_body.go — Vertex-on-Anthropic 出站 body reshape。
//
// Vertex AI 的 Anthropic rawPredict/streamRawPredict 端点期望一个与 Anthropic
// Messages API 高度一致、但有三处差异的 body：
//   - 不能携带顶层 "model"（model 已编码进 URL path 的 publishers/anthropic/
//     models/{model} 段）。
//   - 不能携带顶层 "stream"（流式由 URL action 切换 rawPredict vs
//     streamRawPredict + ?alt=sse，body 内的 stream 字段会被上游拒）。
//   - 必须携带顶层 "anthropic_version": "vertex-2023-10-16"（Vertex 平台标识，
//     与 Bedrock 的 "bedrock-2023-05-31" 同位但取值不同）。
//
// 其余字段（messages / max_tokens / system / tools / temperature 等）原样
// carry-over，未知字段也透传，保字段透传一致语义。
//
// fail-closed：body 非合法 JSON object 时返回 error，绝不把坏 body 发出去。
package vertex

import (
	"encoding/json"
	"errors"
	"fmt"
)

// AnthropicVersionVertex 是 Vertex 平台要求的 anthropic_version 字段值。
const AnthropicVersionVertex = "vertex-2023-10-16"

// ErrEmptyAnthropicBody 表示输入 body 为空。
var ErrEmptyAnthropicBody = errors.New("vertex: Anthropic body 为空")

// ErrInvalidAnthropicJSON 表示输入 body 不是合法 JSON object。
var ErrInvalidAnthropicJSON = errors.New("vertex: Anthropic body 不是合法 JSON object")

// reshapeAnthropicBody 把标准 Anthropic Messages body 改写成 Vertex Anthropic
// rawPredict/streamRawPredict 期望形态：
//  1. 剥离顶层 "model"（URL path 已带 model）。
//  2. 剥离顶层 "stream"（流式由 URL action 决定）。
//  3. 注入 "anthropic_version" = "vertex-2023-10-16"（已显式声明则尊重 caller）。
//  4. 其余字段全保留。
//
// 顶层非 JSON object（含 "null" / 数组 / 截断字节）→ 返回 error，fail-closed。
func reshapeAnthropicBody(anthropicBody []byte) ([]byte, error) {
	if len(anthropicBody) == 0 {
		return nil, ErrEmptyAnthropicBody
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(anthropicBody, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAnthropicJSON, err)
	}
	// json.Unmarshal("null", &raw) 不报错但 raw 为 nil；后续 map 写会 panic。
	if raw == nil {
		return nil, fmt.Errorf("%w: 顶层为 null 或非 object", ErrInvalidAnthropicJSON)
	}

	delete(raw, "model")
	delete(raw, "stream")

	// 注入 anthropic_version（caller 已显式声明则不覆盖）。
	if _, exists := raw["anthropic_version"]; !exists {
		raw["anthropic_version"] = json.RawMessage(`"` + AnthropicVersionVertex + `"`)
	}

	out, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("vertex: Anthropic body 重序列化失败: %w", err)
	}
	return out, nil
}
