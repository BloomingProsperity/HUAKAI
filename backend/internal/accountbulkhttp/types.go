// Package accountbulkhttp 提供按选中账号 ID 的批量运维入口:
//
//	POST /admin/v1/provider-accounts/bulk
//
// 一次请求对一组显式选中的账号执行同一个动作(启停、改优先级/权重、清限流、完整恢复、立刻刷新凭据、
// 改渠道),每个账号独立成败、逐项返回稳定结果码,已成功项不会因为其他项失败而回滚。选中 ID 与
// "按标签筛选"是两套入口(后者是 bulk-by-tag),不允许用当前筛选默默替代勾选。
//
// 权限只来自认证上下文与库内归属:租户管理员锁本租户,部署者必须显式给出 tenant_id;不属于该租户、
// 已删除或不存在的 ID 一律 not_found,不区分。
package accountbulkhttp

import "time"

// Action 是批量动作。
type Action string

const (
	ActionSetEnabled        Action = "set_enabled"
	ActionSetPriority       Action = "set_priority"
	ActionSetStaticWeight   Action = "set_static_weight"
	ActionClearRateLimit    Action = "clear_rate_limit"
	ActionRecover           Action = "recover"
	ActionRefreshCredential Action = "refresh_credential"
	ActionMoveChannel       Action = "move_channel"
)

// 批次上限与执行参数的默认值。刷新会打上游且受风暴预算约束,单批更小、并发受限。
const (
	DefaultMaxIDs           = 200
	DefaultMaxRefreshIDs    = 50
	DefaultRefreshWorkers   = 4
	DefaultBatchTimeout     = 60 * time.Second
	DefaultRefreshItemLimit = 30 * time.Second
)

// Request 是批量请求体。
type Request struct {
	IDs              []int64 `json:"ids"`
	Action           Action  `json:"action"`
	Enabled          *bool   `json:"enabled,omitempty"`
	Priority         *int32  `json:"priority,omitempty"`
	StaticWeight     *int32  `json:"static_weight,omitempty"`
	ChannelID        *int64  `json:"channel_id,omitempty"`
	ConfirmMixedRisk bool    `json:"confirm_mixed_risk,omitempty"`
	Reason           string  `json:"reason,omitempty"`
	DryRun           bool    `json:"dry_run,omitempty"`
}

// 逐项状态。
const (
	StatusSucceeded   = "succeeded"    // 已写入
	StatusSkipped     = "skipped"      // 无需写入(已达期望态 / 无状态可清)
	StatusDeferred    = "deferred"     // 被风暴预算或互斥推迟,后台会补齐
	StatusFailed      = "failed"       // 该项失败,见 code
	StatusCancelled   = "cancelled"    // 批次超时,该项未开始
	StatusWouldApply  = "would_apply"  // dry-run:将会写入
	StatusWouldReject = "would_reject" // dry-run:将会拒绝
)

// 逐项结果码(稳定、可编程)。
const (
	CodeNotFound                 = "not_found"                        // 不存在 / 他租户 / 已删除
	CodeAlreadyInDesiredState    = "already_in_desired_state"         // 已达期望态
	CodeStateNotAllowed          = "state_not_allowed"                // 账号状态不允许该动作(如已撤销)
	CodeRefreshInProgress        = "refresh_in_progress"              // 刷新槽被后台/他副本持有
	CodeRefreshDeferred          = "refresh_deferred"                 // 风暴预算推迟
	CodeRefreshNotApplicable     = "refresh_not_applicable"           // 静态凭据 / 无刷新资格
	CodeRefreshFailed            = "refresh_failed"                   // 刷新执行失败(detail 含分类)
	CodeChannelNotFound          = "channel_not_found"                // 目标渠道不在本租户或已删除
	CodeMixedRiskConfirmRequired = "mixed_risk_confirmation_required" // 目标渠道混合风险需确认
	CodePartialRecovery          = "partial_recovery"                 // 账号侧已清、渠道侧失败,可重试
	CodeUpstreamStoreError       = "store_error"                      // 数据库/事务错误,可重试
	CodeBatchTimeout             = "batch_timeout"                    // 批次超时未开始
	CodeDependencyNotConfigured  = "dependency_not_configured"        // 该动作的依赖未装配
	CodeInvalidTargetSameChannel = "already_in_channel"               // 已在目标渠道
)

// ItemResult 是单个账号的结果。Detail 只放非秘密的结构化说明(如混合风险报告、刷新结论)。
type ItemResult struct {
	ID        int64          `json:"id"`
	Status    string         `json:"status"`
	Code      string         `json:"code,omitempty"`
	Message   string         `json:"message,omitempty"`
	Retryable bool           `json:"retryable"`
	Detail    map[string]any `json:"detail,omitempty"`
}

// Response 是批次结果。PartialSuccess 在既有成功又有失败/推迟时为 true;
// SucceededNotRolledBack 永远为 true:没有跨账号事务,已成功项不会因其他项失败而回滚。
type Response struct {
	Action                 Action       `json:"action"`
	DryRun                 bool         `json:"dry_run"`
	TenantID               int64        `json:"tenant_id"`
	Total                  int          `json:"total"`
	Succeeded              int          `json:"succeeded"`
	Skipped                int          `json:"skipped"`
	Deferred               int          `json:"deferred"`
	Failed                 int          `json:"failed"`
	Cancelled              int          `json:"cancelled"`
	PartialSuccess         bool         `json:"partial_success"`
	SucceededIDs           []int64      `json:"succeeded_ids"`
	FailedIDs              []int64      `json:"failed_ids"`
	SucceededNotRolledBack bool         `json:"succeeded_not_rolled_back"`
	DuplicateIDsRemoved    int          `json:"duplicate_ids_removed"`
	BatchAuditID           *int64       `json:"batch_audit_id,omitempty"`
	Results                []ItemResult `json:"results"`
}
