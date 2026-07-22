package proto

import "encoding/json"

// TextNode 是 text capability 的 payload；复用 CanonicalContentBlock 表达 text 内容。
type TextNode struct {
	Role  string                `json:"role"`
	Block CanonicalContentBlock `json:"block"`
}

// DataSourceKind 标记 file/image/video 数据来源形态。
type DataSourceKind string

const (
	DataSourceInlineBase64 DataSourceKind = "inline_base64"
	DataSourceURL          DataSourceKind = "url"
	DataSourceFileID       DataSourceKind = "file_id"
	DataSourceDigestRef    DataSourceKind = "digest_ref"
)

// DataLocator 描述数据物理位置。
type DataLocator struct {
	Kind  DataSourceKind `json:"kind"`
	Value string         `json:"value"`
}

// MediaDimensions 是图片/视频维度。
type MediaDimensions struct {
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// TimeRange 是视频/音频剪辑范围（毫秒）。
type TimeRange struct {
	StartMillis int64 `json:"start_ms,omitempty"`
	EndMillis   int64 `json:"end_ms,omitempty"`
}

// FileNode 是 file capability 的 payload。
type FileNode struct {
	SourceKind DataSourceKind `json:"source_kind"`
	MediaType  string         `json:"media_type"`
	Locator    DataLocator    `json:"locator"`
	SizeBytes  int64          `json:"size_bytes,omitempty"`
	Digest     string         `json:"digest,omitempty"`
	Retention  string         `json:"retention,omitempty"`
}

// CacheScope 标记 cache_control 作用域。
type CacheScope string

const (
	CacheScopeRequest CacheScope = "request"
	CacheScopeMessage CacheScope = "message"
	CacheScopeBlock   CacheScope = "block"
	CacheScopeSession CacheScope = "session"
	CacheScopeVendor  CacheScope = "vendor"
)

// CacheControlNode 是 cache_control capability 的 payload。
type CacheControlNode struct {
	Scope                    CacheScope `json:"scope"`
	BreakpointRefs           []string   `json:"breakpoint_refs"`
	CacheKeyHint             string     `json:"cache_key_hint,omitempty"`
	CacheCreationInputTokens int        `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int        `json:"cache_read_input_tokens,omitempty"`
	SanitizeSystemMetadata   bool       `json:"sanitize_system_metadata"`
	LocalityHint             string     `json:"locality_hint,omitempty"`
}

// DataRetentionLabel 锁定数据保留标识。
type DataRetentionLabel string

const (
	DataRetentionUnknown                  DataRetentionLabel = "unknown"
	DataRetentionRequestStoreFalse        DataRetentionLabel = "request_store_false"
	DataRetentionProviderContractRequired DataRetentionLabel = "provider_contract_required"
	DataRetentionRegionalAsserted         DataRetentionLabel = "regional_asserted"
	DataRetentionZDRVerified              DataRetentionLabel = "zdr_verified"
)

var AllDataRetentionLabels = []DataRetentionLabel{
	DataRetentionUnknown,
	DataRetentionRequestStoreFalse,
	DataRetentionProviderContractRequired,
	DataRetentionRegionalAsserted,
	DataRetentionZDRVerified,
}

type DataRetentionValue = DataRetentionLabel

// DataRetentionNode 是 data_retention capability 的 payload。
type DataRetentionNode struct {
	Value        DataRetentionLabel `json:"value"`
	Enforcement  string             `json:"enforcement"`
	Region       string             `json:"region,omitempty"`
	RequestStore *bool              `json:"request_store,omitempty"`
	NoTrain      bool               `json:"no_train,omitempty"`
	EvidenceRef  string             `json:"evidence_ref,omitempty"`
	AuditLabel   string             `json:"audit_label"`
}

// StructuredOutputMode 是结构化输出模式。
type StructuredOutputMode string

const (
	StructuredOutputJSONMode       StructuredOutputMode = "json_mode"
	StructuredOutputJSONSchema     StructuredOutputMode = "json_schema"
	StructuredOutputToolStrategy   StructuredOutputMode = "tool_strategy"
	StructuredOutputProviderNative StructuredOutputMode = "provider_native"
)

// StructuredOutputNode 是 structured_output capability 的 payload。
type StructuredOutputNode struct {
	Mode             StructuredOutputMode `json:"mode"`
	Strict           bool                 `json:"strict"`
	Schema           json.RawMessage      `json:"schema"`
	ParserMode       string               `json:"parser_mode,omitempty"`
	FailureRecovery  string               `json:"failure_recovery,omitempty"`
	FallbackStrategy string               `json:"fallback_strategy,omitempty"`
}

// RedactionClass 标记 reasoning/thinking 内容的可见性策略。
type RedactionClass string

const (
	RedactionPublic       RedactionClass = "public"
	RedactionRedacted     RedactionClass = "redacted"
	RedactionHidden       RedactionClass = "hidden"
	RedactionProviderOnly RedactionClass = "provider_only"
)

// ThinkingNode 是 thinking capability 的 payload。
type ThinkingNode struct {
	Mode         string                  `json:"mode,omitempty"`
	BudgetTokens int                     `json:"budget_tokens"`
	Blocks       []CanonicalContentBlock `json:"blocks"`
	HiddenTokens int                     `json:"hidden_tokens,omitempty"`
	Signature    string                  `json:"signature,omitempty"`
	Redaction    RedactionClass          `json:"redaction"`
}

// BatchStatus 标记 batch 作业状态。
type BatchStatus string

const (
	BatchPending   BatchStatus = "pending"
	BatchValidated BatchStatus = "validated"
	BatchFailed    BatchStatus = "failed"
	BatchComplete  BatchStatus = "complete"
)

// RetryPolicy 是 batch/job 重试策略。
type RetryPolicy struct {
	MaxAttempts int    `json:"max_attempts,omitempty"`
	Backoff     string `json:"backoff,omitempty"`
}

// BatchNode 是 batch capability 的 payload。
type BatchNode struct {
	JobID           string       `json:"job_id"`
	Endpoint        string       `json:"endpoint"`
	InputRef        string       `json:"input_ref"`
	Validation      BatchStatus  `json:"validation"`
	OutputRef       string       `json:"output_ref,omitempty"`
	ErrorRef        string       `json:"error_ref,omitempty"`
	RetryPolicy     *RetryPolicy `json:"retry_policy,omitempty"`
	CostAttribution string       `json:"cost_attribution,omitempty"`
}

// LiveTransport 标记 live_session 传输协议。
type LiveTransport string

const (
	LiveTransportWSS LiveTransport = "wss"
	LiveTransportSSE LiveTransport = "sse"
)

// LiveSessionNode 是 live_session capability 的 payload。
type LiveSessionNode struct {
	SessionID     string          `json:"session_id"`
	Transport     LiveTransport   `json:"transport"`
	ConnectParams json.RawMessage `json:"connect_params,omitempty"`
	Modalities    []string        `json:"modalities"`
	ToolNodeIDs   []string        `json:"tool_node_ids,omitempty"`
	ResumeToken   string          `json:"resume_token,omitempty"`
	CloseReason   string          `json:"close_reason,omitempty"`
}
