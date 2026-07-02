package proto

// ImageNode 是 image capability 的 payload。
type ImageNode struct {
	// SourceKind 必填；inline_base64/url/file_id/digest_ref。
	SourceKind DataSourceKind `json:"source_kind"`

	// MediaType：inline_base64 时必填(如 image/png,已归一为主类型小写);
	// url 形态可空(OpenAI image_url 无独立 mime 字段,由上游按 URL 自判)。
	MediaType string `json:"media_type"`

	// Locator 必填；图片数据位置或 provider file id。
	Locator DataLocator `json:"locator"`

	// Dimensions 可选；未知时省略。
	Dimensions *MediaDimensions `json:"dimensions,omitempty"`
}
