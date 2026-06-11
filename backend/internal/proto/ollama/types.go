// 包 ollama — Ollama 原生 /api/chat 协议的 DTO 与 HCSF 转换。
//
// 协议要点（与 OpenAI 兼容形态的关键差异）：
//   - 采样参数不在顶层：temperature/top_p/seed/stop/num_predict（max_tokens
//     的改名）全部嵌进 options{}；顶层出现这些字段即协议污染。
//   - Ollama 默认 stream=true：非流式请求必须显式写 "stream": false，
//     漏写=非流请求被流式 NDJSON 响应打爆。
//   - 流式 wire 是 NDJSON：逐行一个裸 JSON 对象，无 "data:" 前缀、无 [DONE]
//     哨兵、无 event: 行；终帧 done:true 携带 done_reason 与 usage 计数
//     （prompt_eval_count / eval_count）。
//   - 多模态：images 是 base64 字符串数组，无 URL 形态。
//   - assistant 消息的 tool_calls.function.arguments 是 JSON 对象（不是
//     OpenAI 形的字符串）；tool 结果消息 role="tool"。
package ollama

import "encoding/json"

// chatRequest 是 /api/chat 请求 body。
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	// Stream 必须显式序列化（无 omitempty）：Ollama 缺省按 true 处理，
	// 非流式请求漏写该字段会收到 NDJSON 流而不是单 JSON。
	Stream  bool           `json:"stream"`
	Options map[string]any `json:"options,omitempty"`
	Tools   []requestTool  `json:"tools,omitempty"`
	// Format 是结构化输出开关："json" 字符串或 JSON schema 对象。
	Format json.RawMessage `json:"format,omitempty"`
	// Think 开关思维链(Ollama /api/chat 顶层 "think")。nil=不下发(模型默认),
	// canonical thinking 节点存在时置 true。
	Think *bool `json:"think,omitempty"`
}

// chatMessage 是请求 messages 数组元素。
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Images 是 base64 图片数组（仅 user 消息有效；无 URL 形态）。
	Images    []string   `json:"images,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

// toolCall 是 assistant 消息 / 响应帧里的工具调用。
type toolCall struct {
	Function toolCallFunction `json:"function"`
}

// toolCallFunction 的 Arguments 是 JSON 对象（与 OpenAI 的字符串编码不同）。
type toolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// requestTool 是顶层 tools 数组元素（OpenAI 形 function 声明）。
type requestTool struct {
	Type     string              `json:"type"`
	Function requestToolFunction `json:"function"`
}

type requestToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// chatResponse 同时是非流式响应体与流式 NDJSON 单帧：
//   - done:false 帧：Message 携带 content/thinking 增量或完整 tool_calls
//   - done:true 终帧：DoneReason + PromptEvalCount/EvalCount（usage 只在终帧）
//   - 非流式：单 JSON 对象，同终帧形 + Message.Content 为全文
type chatResponse struct {
	Model           string       `json:"model"`
	CreatedAt       string       `json:"created_at"`
	Message         *respMessage `json:"message,omitempty"`
	Done            bool         `json:"done"`
	DoneReason      string       `json:"done_reason,omitempty"`
	PromptEvalCount int          `json:"prompt_eval_count,omitempty"`
	EvalCount       int          `json:"eval_count,omitempty"`
	// Error 是 Ollama 中流错误通道:HTTP 200 已提交后上游以单行
	// {"error":"..."} 报致命错误。必须 fail-loud,绝不能当空帧吞掉
	// (静默截断会伪装成正常终止)。
	Error string `json:"error,omitempty"`
}

// respMessage 是响应 message 载荷；Thinking 为思维链增量（不计入答案正文）。
type respMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}
