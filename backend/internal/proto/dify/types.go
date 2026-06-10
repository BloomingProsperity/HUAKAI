// 包 dify — Dify 应用 API（chat-messages / workflows/run / completion-messages）
// 的协议 DTO 与 HCSF 转换。
//
// Dify 协议要点（官方 API 契约）：
//   - 请求是单 query 字符串 + inputs 变量表；无 per-request 模型/采样参数
//     （model / max_tokens / temperature 等全在 Dify app 侧配置）。
//   - user 字段必填（Dify 用它做终端用户会话归属）。
//   - 流式 SSE 的事件名在 data JSON 的 "event" 字段里；message_end 携带 usage
//     表示流自然结束，没有 [DONE] 哨兵。
package dify

// chatRequest 是 Dify 会话请求 body（chat-messages / completion-messages /
// workflows/run 同形主干）。
type chatRequest struct {
	// Inputs 是 app 变量表；HUAKAI 投影恒为空对象（变量由 app 侧定义）。
	Inputs map[string]any `json:"inputs"`
	// Query 是把整个对话折叠后的单字符串上下文。
	Query string `json:"query"`
	// ResponseMode 是 "streaming" 或 "blocking"。
	ResponseMode string `json:"response_mode"`
	// User 是终端用户标识，Dify 必填。
	User string `json:"user"`
	// AutoGenerateName 关闭 Dify 侧自动起会话名（网关代理无此需求）。
	AutoGenerateName bool `json:"auto_generate_name"`
	// Files 是可选输入文件（v1 仅远程 URL 图片）。
	Files []requestFile `json:"files,omitempty"`
}

// requestFile 是 Dify 请求 files 数组元素。v1 适配器契约禁止子请求上传，
// 因此只支持 remote_url 传输方式。
type requestFile struct {
	Type           string `json:"type"`
	TransferMethod string `json:"transfer_method"`
	URL            string `json:"url"`
}

// chatResponse 是非流式（blocking）响应 body。
type chatResponse struct {
	ConversationID string           `json:"conversation_id"`
	Answer         string           `json:"answer"`
	Metadata       responseMetadata `json:"metadata"`
}

// responseMetadata 承载 message_end / blocking 响应里的 usage。
type responseMetadata struct {
	Usage *usagePayload `json:"usage,omitempty"`
}

// usagePayload 是 Dify usage 计量（OpenAI 同名字段形态）。
type usagePayload struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// streamChunk 是流式 SSE 单帧 data JSON。Event 决定其余字段哪些有效：
//   - message / agent_message: Answer 为增量文本
//   - message_end:             Metadata.Usage 为终态计量
//   - error:                   Status/Code/Message 描述上游错误
//   - workflow_* / node_* 等编排事件不含用户文本
type streamChunk struct {
	Event          string            `json:"event"`
	Answer         string            `json:"answer"`
	ConversationID string            `json:"conversation_id"`
	Metadata       *responseMetadata `json:"metadata,omitempty"`
	Status         int               `json:"status,omitempty"`
	Code           string            `json:"code,omitempty"`
	Message        string            `json:"message,omitempty"`
}
