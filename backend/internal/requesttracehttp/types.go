package requesttracehttp

import (
	"encoding/json"
	"regexp"
)

// Viewer 是读投影的脱敏档位。
type Viewer string

const (
	// ViewerUser 最终用户:模型、池、状态、用量与费用;不含账号代号、渠道、选号明细、账号级审计。
	ViewerUser Viewer = "user"
	// ViewerTenant 租户管理员:本租户资源代号(账号 id/名称、渠道 id)、已脱敏的尝试明细与账号级审计。
	ViewerTenant Viewer = "tenant_operator"
	// ViewerPlatform 部署者:全部代号,含租户/用户/Key 的数字标识与中止原因分类。
	ViewerPlatform Viewer = "platform_admin"
)

// 请求标识可能是网关 HTTP 请求标识(UUID)、客户端幂等键(任意可打印文本)或模型回退派生标识;
// 与收据接口同一口径:非空、不超过 MaxRequestIDLength 字节、不含空白与控制字符、至多一个斜杠。
const MaxRequestIDLength = 256

var requestIDForbidden = regexp.MustCompile(`[\s\x00-\x1f\x7f]`)

// Response 是时间线投影。
type Response struct {
	Viewer   Viewer      `json:"viewer"`
	Request  RequestHead `json:"request"`
	Attempts []Attempt   `json:"attempts"`
	Events   []Event     `json:"events"`
	Receipt  *Receipt    `json:"receipt"`
	// CostReceipt 是用户面签名费用收据摘要;没有则为 null。
	CostReceipt *CostReceipt `json:"cost_receipt"`
	Coverage    Coverage     `json:"coverage"`
	Totals      AttemptUsage `json:"totals"`
}

// RequestHead 是请求头信息(来自账本 claim,永久保留)。
type RequestHead struct {
	// RequestID 是查询所用标识;LogicalRequestID 是账本幂等标识;HTTPRequestIDs 是响应头 / 收据 / 审计
	// 使用的网关请求标识。
	RequestID        string   `json:"request_id"`
	LogicalRequestID string   `json:"logical_request_id"`
	HTTPRequestIDs   []string `json:"http_request_ids"`
	TenantID         *int64   `json:"tenant_id,omitempty"`
	UserID           *int64   `json:"user_id,omitempty"`
	APIKeyID         *int64   `json:"api_key_id,omitempty"`
	EndpointFamily   string   `json:"endpoint_family"`
	RequestedModel   string   `json:"requested_model"`
	RequestClass     string   `json:"request_class"`
	BillingEffect    string   `json:"billing_effect"`
	PoolGroupID      *int64   `json:"pool_group_id,omitempty"`
	PoolName         *string  `json:"pool_name,omitempty"`
	Status           string   `json:"status"`
	// FailureCategory 是最终用户也能理解的粗分类(auth / routing_unavailable / upstream / internal /
	// rate_limit / quota / invalid_request / security_policy / other),只在请求已中止时给出;
	// 原始分类码 AbortedReason 只给运营侧。
	FailureCategory string  `json:"failure_category,omitempty"`
	AbortedReason   *string `json:"aborted_reason,omitempty"`
	LastAttemptSeq  int32   `json:"last_attempt_seq"`
	// FinalAccount 是最后一次尝试的账号代号(用户不可见)。
	FinalAccount  *AccountRef `json:"final_account,omitempty"`
	PredictedCost string      `json:"predicted_cost"`
	ActualCost    *string     `json:"actual_cost"`
	Currency      string      `json:"currency"`
	ReservedAt    string      `json:"reserved_at"`
	SettledAt     *string     `json:"settled_at"`
}

// AccountRef 是账号代号:租户内资源 id 与运营命名,不含凭据、登录名或上游地址。
type AccountRef struct {
	ProviderAccountID int64   `json:"provider_account_id"`
	Name              *string `json:"name,omitempty"`
	ChannelID         *int64  `json:"channel_id,omitempty"`
}

// Attempt 是一次已结算的上游尝试。
type Attempt struct {
	AttemptSeq int32  `json:"attempt_seq"`
	Outcome    string `json:"outcome"`
	EndClass   string `json:"end_class"`
	// FailureCategory 是失败/部分/取消尝试的粗分类(全部角色);TerminatedReason 是网关记录的中止分类码(运营侧)。
	FailureCategory       string         `json:"failure_category,omitempty"`
	TerminatedReason      *string        `json:"terminated_reason,omitempty"`
	Provider              *string        `json:"provider,omitempty"`
	Account               *AccountRef    `json:"account,omitempty"`
	RequestedModel        string         `json:"requested_model"`
	UpstreamModel         *string        `json:"upstream_model,omitempty"`
	Stream                bool           `json:"stream"`
	UsageSource           string         `json:"usage_source"`
	SettlementSource      string         `json:"settlement_source"`
	PendingReconciliation bool           `json:"pending_reconciliation"`
	Usage                 AttemptUsage   `json:"usage"`
	Timing                AttemptTiming  `json:"timing"`
	Routing               *RoutingDigest `json:"routing,omitempty"`
	ProtocolLossCount     int            `json:"protocol_loss_count"`
}

// AttemptUsage 是用量与费用分项。费用以十进制字符串表示。
type AttemptUsage struct {
	TokensInput         int64  `json:"tokens_input"`
	TokensOutput        int64  `json:"tokens_output"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	ImageOutputTokens   int64  `json:"image_output_tokens"`
	DeliveredTokens     int64  `json:"delivered_tokens"`
	ActualCost          string `json:"actual_cost"`
	InputCost           string `json:"input_cost"`
	OutputCost          string `json:"output_cost"`
	CacheCreationCost   string `json:"cache_creation_cost"`
	CacheReadCost       string `json:"cache_read_cost"`
	ImageOutputCost     string `json:"image_output_cost"`
}

// AttemptTiming 是尝试的时间点(RFC3339,缺失为 null)。
type AttemptTiming struct {
	RequestedAt       string  `json:"requested_at"`
	UpstreamRequestAt *string `json:"upstream_request_at"`
	FirstByteAt       *string `json:"first_byte_at"`
	FirstEventAt      *string `json:"first_event_at"`
	LastEventAt       *string `json:"last_event_at"`
	SettledAt         string  `json:"settled_at"`
}

// RoutingDigest 是选号解释的脱敏摘要。租户管理员与部署者可见;按排除原因的候选计数不含账号 id,
// 逐账号排除项只回传给部署者。
type RoutingDigest struct {
	SelectionLayer      string            `json:"selection_layer"`
	AffinityKeyClass    string            `json:"affinity_key_class,omitempty"`
	StickyBreakReason   *string           `json:"sticky_break_reason,omitempty"`
	CapabilityOutcome   string            `json:"capability_outcome,omitempty"`
	RetryAttemptNumber  int               `json:"retry_attempt_number"`
	ExclusionCounts     map[string]int    `json:"candidate_counts_by_exclusion,omitempty"`
	ExcludedAccounts    []ExcludedAccount `json:"excluded_accounts,omitempty"`
	ScoringPolicy       string            `json:"scoring_policy_version,omitempty"`
	WaitedInQueue       bool              `json:"waited_in_queue"`
	QueueExitReason     string            `json:"queue_exit_reason,omitempty"`
	ForcedRouteOverride bool              `json:"forced_route_override"`
}

// ExcludedAccount 是选号时被排除的账号及原因(仅部署者)。
type ExcludedAccount struct {
	ProviderAccountID int64  `json:"provider_account_id"`
	Reason            string `json:"reason"`
}

// Event 是时间线子记录:账本事件(全部角色)与账号级审计事件(租户管理员与部署者)。
type Event struct {
	Source        string      `json:"source"`
	EventType     string      `json:"event_type"`
	Reason        string      `json:"reason,omitempty"`
	PreviousState string      `json:"previous_state,omitempty"`
	NewState      string      `json:"new_state,omitempty"`
	EndClass      *string     `json:"end_class,omitempty"`
	UsageSource   *string     `json:"usage_source,omitempty"`
	ActualCost    *string     `json:"actual_cost,omitempty"`
	Account       *AccountRef `json:"account,omitempty"`
	OccurredAt    string      `json:"occurred_at"`
}

// Receipt 是信任链收据摘要。hop_chain 含上游跳点代号,仅部署者可见。
type Receipt struct {
	LedgerID          string          `json:"ledger_id"`
	OccurredAt        string          `json:"occurred_at"`
	PubkeyFingerprint string          `json:"pubkey_fingerprint"`
	ModelChain        json.RawMessage `json:"model_chain,omitempty"`
	HopChain          json.RawMessage `json:"hop_chain,omitempty"`
}

// Coverage 是降级标记:哪些事实段存在、尝试明细完整度、明细保留状态。
type Coverage struct {
	HasUsage              bool     `json:"has_usage"`
	HasBillingEvents      bool     `json:"has_billing_events"`
	HasAuditEvents        bool     `json:"has_audit_events"`
	HasReceipt            bool     `json:"has_receipt"`
	HasCostReceipt        bool     `json:"has_cost_receipt"`
	AttemptsDetail        string   `json:"attempts_detail"`
	DetailRetention       string   `json:"detail_retention"`
	UnknownAttemptAccount int      `json:"unknown_account_attempts"`
	AbortedAttempts       int      `json:"aborted_attempts"`
	CommittedAttempts     int      `json:"committed_attempts"`
	Notes                 []string `json:"notes"`
}

// 尝试明细完整度与明细保留状态的取值。
const (
	AttemptsDetailFull      = "full"       // 每个账本尝试都有用量行
	AttemptsDetailPartial   = "partial"    // 部分尝试只有账本事件(拿到账号前中止),账号未知
	AttemptsDetailFinalOnly = "final_only" // 没有任何用量行,只能给出账本头里的最后一跳
	AttemptsDetailNone      = "none"       // 连账本事件也没有

	DetailRetentionWithin   = "within"    // 用量明细仍在保留期内
	DetailRetentionPending  = "pending"   // 刚结算,用量明细还在异步落盘
	DetailRetentionExpired  = "expired"   // 有已提交账本事件却无用量行:明细已过保留期或落盘失败
	DetailRetentionNoDetail = "no_detail" // 请求没有产生过用量明细(全部在拿到账号前中止)
)

// CostReceipt 是面向用户的签名费用收据摘要(与 /v1/receipts 同源),全部角色可见。
type CostReceipt struct {
	RequestID       string `json:"request_id"`
	ReceiptSequence int32  `json:"receipt_sequence"`
	Model           string `json:"model"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	CostUSDMicros   int64  `json:"cost_usd_micros"`
	CreatedAt       string `json:"created_at"`
}
