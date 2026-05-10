package proto

// ImageNode 是 image capability 的 payload。
type ImageNode struct {
	// SourceKind 必填；inline_base64/url/file_id/digest_ref。
	SourceKind DataSourceKind `json:"source_kind"`

	// MediaType 必填；MIME type，如 image/png。
	MediaType string `json:"media_type"`

	// Locator 必填；图片数据位置或 provider file id。
	Locator DataLocator `json:"locator"`

	// Dimensions 可选；未知时省略。
	Dimensions *MediaDimensions `json:"dimensions,omitempty"`
}
