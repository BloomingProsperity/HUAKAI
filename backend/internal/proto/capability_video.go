package proto

// VideoNode 是 video capability 的 payload。
type VideoNode struct {
	// SourceKind 必填；inline_base64/url/file_id/digest_ref。
	SourceKind DataSourceKind `json:"source_kind"`

	// MediaType 必填；MIME type，如 video/mp4。
	MediaType string `json:"media_type"`

	// Locator 必填；视频数据位置或 provider file id。
	Locator DataLocator `json:"locator"`

	// Dimensions 可选；未知时省略。
	Dimensions *MediaDimensions `json:"dimensions,omitempty"`

	// TimeRange 可选；剪辑范围，单位毫秒。
	TimeRange *TimeRange `json:"time_range,omitempty"`

	// Codec 可选；如 h264/vp9/av1。
	Codec string `json:"codec,omitempty"`

	// SizeBytes 可选；0 表示未知。
	SizeBytes int64 `json:"size_bytes,omitempty"`
}
