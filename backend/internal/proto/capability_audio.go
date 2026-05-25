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
	// Transport 必填；inline/file/url/stream。
	Transport MediaTransport `json:"transport"`

	// Format 必填；如 wav/mp3/opus/pcm16。
	Format string `json:"format"`

	// Locator 必填；音频数据位置或 stream ref。
	Locator DataLocator `json:"locator"`

	// SampleRateHz 可选；0 表示未知。
	SampleRateHz int `json:"sample_rate_hz,omitempty"`

	// Channels 可选；0 表示未知。
	Channels int `json:"channels,omitempty"`

	// DurationMillis 可选；0 表示未知。
	DurationMillis int64 `json:"duration_ms,omitempty"`

	// TranscriptPolicy 可选；none/requested/provided。
	TranscriptPolicy TranscriptPolicy `json:"transcript_policy,omitempty"`

	// LiveCompatible 必填；默认 false；true 表示可连接 live_session。
	LiveCompatible bool `json:"live_compatible"`
}
