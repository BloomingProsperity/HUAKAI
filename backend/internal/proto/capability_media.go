package proto

// MediaTransport 标记音频/视频传输形态。
type MediaTransport string

const (
	MediaTransportInline MediaTransport = "inline"
	MediaTransportFile   MediaTransport = "file"
	MediaTransportURL    MediaTransport = "url"
	MediaTransportStream MediaTransport = "stream"
)

// TranscriptPolicy 标记音频转写策略。
type TranscriptPolicy string

const (
	TranscriptNone      TranscriptPolicy = "none"
	TranscriptRequested TranscriptPolicy = "requested"
	TranscriptProvided  TranscriptPolicy = "provided"
)

// AudioNode 是 audio capability 的 payload。
type AudioNode struct {
	Transport        MediaTransport   `json:"transport"`
	Format           string           `json:"format"`
	Locator          DataLocator      `json:"locator"`
	SampleRateHz     int              `json:"sample_rate_hz,omitempty"`
	Channels         int              `json:"channels,omitempty"`
	DurationMillis   int64            `json:"duration_ms,omitempty"`
	TranscriptPolicy TranscriptPolicy `json:"transcript_policy,omitempty"`
	LiveCompatible   bool             `json:"live_compatible"`
}

// ImageNode 是 image capability 的 payload。
type ImageNode struct {
	SourceKind DataSourceKind   `json:"source_kind"`
	MediaType  string           `json:"media_type"`
	Locator    DataLocator      `json:"locator"`
	Dimensions *MediaDimensions `json:"dimensions,omitempty"`
}

// VideoNode 是 video capability 的 payload。
type VideoNode struct {
	SourceKind DataSourceKind   `json:"source_kind"`
	MediaType  string           `json:"media_type"`
	Locator    DataLocator      `json:"locator"`
	Dimensions *MediaDimensions `json:"dimensions,omitempty"`
	TimeRange  *TimeRange       `json:"time_range,omitempty"`
	Codec      string           `json:"codec,omitempty"`
	SizeBytes  int64            `json:"size_bytes,omitempty"`
}
