// 包 geminicodeassist — Gemini Code Assist（cloudcode-pa v1internal）入站响应
// 解析适配器。
//
// cloudcode-pa 把每个响应（非流式 body 与每个流式 SSE chunk）都包一层
// {"response":{<gemini body>}}。本包的 Adapter unwrap 该外层 "response" 后，
// 委托既有 proto/gemini.Adapter 做标准 Gemini SSE/JSON → canonical 解析——
// 不复制 gemini 解析逻辑，只组合它。
//
// 不碰冻结的 proto/gemini 包内文件，仅组合（embed）其导出的 *gemini.Adapter。
package geminicodeassist

import "encoding/json"

// codeAssistResponseEnvelope 是 cloudcode-pa 的响应外层 envelope。
//
// Response 用 json.RawMessage 携带内层 gemini body，避免重 marshal。
type codeAssistResponseEnvelope struct {
	Response json.RawMessage `json:"response"`
}
