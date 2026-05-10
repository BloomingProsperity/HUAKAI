package proto

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
	// MaxAttempts 可选；0 表示未指定。
	MaxAttempts int `json:"max_attempts,omitempty"`

	// Backoff 可选；如 fixed/exponential/provider_default。
	Backoff string `json:"backoff,omitempty"`
}

// BatchNode 是 batch capability 的 payload。
type BatchNode struct {
	// JobID 必填；HUAKAI 或 provider batch job id。
	JobID string `json:"job_id"`

	// Endpoint 必填；batch 目标端点或 native capability。
	Endpoint string `json:"endpoint"`

	// InputRef 必填；指向 file node id、URL 或 provider input file id。
	InputRef string `json:"input_ref"`

	// Validation 必填；pending/validated/failed/complete。
	Validation BatchStatus `json:"validation"`

	// OutputRef 可选；输出文件或结果引用。
	OutputRef string `json:"output_ref,omitempty"`

	// ErrorRef 可选；错误文件或错误结果引用。
	ErrorRef string `json:"error_ref,omitempty"`

	// RetryPolicy 可选；batch/job retry policy。
	RetryPolicy *RetryPolicy `json:"retry_policy,omitempty"`

	// CostAttribution 可选；成本归因标签或 account ref。
	CostAttribution string `json:"cost_attribution,omitempty"`
}
