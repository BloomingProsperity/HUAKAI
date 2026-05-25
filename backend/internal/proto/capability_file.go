package proto

// DataSourceKind 标记 file/image/video 数据来源形态。
type DataSourceKind string

const (
	DataSourceInlineBase64 DataSourceKind = "inline_base64"
	DataSourceURL          DataSourceKind = "url"
	DataSourceFileID       DataSourceKind = "file_id"
	DataSourceDigestRef    DataSourceKind = "digest_ref"
)

// DataLocator 描述数据物理位置；不要求 P-0 解析 provider file lifecycle。
type DataLocator struct {
	// Kind 必填；inline_base64/url/file_id/digest_ref。
	Kind DataSourceKind `json:"kind"`

	// Value 必填；base64 本体、URL、provider file id 或 digest ref。
	Value string `json:"value"`
}

// MediaDimensions 是图片/视频维度。
type MediaDimensions struct {
	// Width 可选；0 表示未知。
	Width int `json:"width,omitempty"`

	// Height 可选；0 表示未知。
	Height int `json:"height,omitempty"`
}

// TimeRange 是视频/音频剪辑范围（毫秒）。
type TimeRange struct {
	// StartMillis 可选；默认 0。
	StartMillis int64 `json:"start_ms,omitempty"`

	// EndMillis 可选；0 表示到媒体结尾或未知。
	EndMillis int64 `json:"end_ms,omitempty"`
}

// FileNode 是 file capability 的 payload。
type FileNode struct {
	// SourceKind 必填；inline_base64/url/file_id/digest_ref。
	SourceKind DataSourceKind `json:"source_kind"`

	// MediaType 必填；MIME type，如 application/pdf。
	MediaType string `json:"media_type"`

	// Locator 必填；不要求 P-0 解析 provider file lifecycle。
	Locator DataLocator `json:"locator"`

	// SizeBytes 可选；0 表示未知。
	SizeBytes int64 `json:"size_bytes,omitempty"`

	// Digest 可选；内容 hash 或外部 digest ref，禁止写明文 secret。
	Digest string `json:"digest,omitempty"`

	// Retention 可选；与 DataRetentionNode.Value 关联的人读标签。
	Retention string `json:"retention,omitempty"`
}
