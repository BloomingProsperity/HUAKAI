package proto

// TextNode 是 text capability 的 payload；复用 CanonicalContentBlock 表达 text 内容。
type TextNode struct {
	// Role 必填；canonical role：user/assistant/system/tool。
	Role string `json:"role"`

	// Block 必填；Block.Type 必须为 "text"。
	Block CanonicalContentBlock `json:"block"`
}
