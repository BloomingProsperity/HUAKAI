package bindingfallback

// Signal 是 executor 或 selector 在完成本地规范化后交给降级决策器的失败信号。
// 它刻意不携带 HTTP 状态码，避免把来源不同的 429、403 或 413 粗暴合并。
type Signal string

const (
	SignalBindingConcurrencyLimit    Signal = "binding_concurrency_limit"
	SignalBindingRateLimit           Signal = "binding_rate_limit"
	SignalPoolCapacityExhausted      Signal = "pool_capacity_exhausted"
	SignalAllChannelsDegraded        Signal = "all_channels_degraded"
	SignalQueueWaitTimeout           Signal = "queue_wait_timeout"
	SignalQueueWaitOverflow          Signal = "queue_wait_overflow"
	SignalUpstreamRateLimit          Signal = "upstream_rate_limit"
	SignalLocalContextWindow         Signal = "local_context_window"
	SignalUpstreamContextWindow      Signal = "upstream_context_window"
	SignalUpstreamContentPolicy      Signal = "upstream_content_policy"
	SignalUpstreamServerError        Signal = "upstream_server_error"
	SignalTransientConnectionFailure Signal = "transient_connection_failure"
	SignalConnectTimeout             Signal = "connect_timeout"
	SignalFirstByteTimeout           Signal = "first_byte_timeout"
	SignalUpstreamTimeout            Signal = "upstream_timeout"
	SignalEmptyResponse              Signal = "empty_response"

	SignalKeyRateLimit                Signal = "key_rate_limit"
	SignalUserQuota                   Signal = "user_quota"
	SignalTenantQuota                 Signal = "tenant_quota"
	SignalTokenQuota                  Signal = "token_quota"
	SignalPoolStaticMismatch          Signal = "pool_static_mismatch"
	SignalQueueWaitCancelled          Signal = "queue_wait_cancelled"
	SignalRequestBodyTooLarge         Signal = "request_body_too_large"
	SignalRequestMalformed            Signal = "request_malformed"
	SignalLocalModerationDenied       Signal = "local_moderation_denied"
	SignalTenantPolicyDenied          Signal = "tenant_policy_denied"
	SignalOfficialClientDenied        Signal = "official_client_denied"
	SignalPermissionDenied            Signal = "permission_denied"
	SignalUpstreamAuthFailure         Signal = "upstream_auth_failure"
	SignalBillingReserveFailure       Signal = "billing_reserve_failure"
	SignalBillingSettleFailure        Signal = "billing_settle_failure"
	SignalBillingAbortFailure         Signal = "billing_abort_failure"
	SignalPricingFailure              Signal = "pricing_failure"
	SignalClaimConflict               Signal = "claim_conflict"
	SignalFingerprintConflict         Signal = "fingerprint_conflict"
	SignalLocalConfigurationFailure   Signal = "local_configuration_failure"
	SignalCredentialResolutionFailure Signal = "credential_resolution_failure"
)

type triggerRule struct {
	target                  Class
	requiresRetryPermission bool
	requiresLocalSafetyPass bool
}

// triggerRules 是唯一的触发归约表。未列出的信号一律由终态门 fail-closed。
var triggerRules = map[Signal]triggerRule{
	SignalBindingConcurrencyLimit:    {target: ClassQuota},
	SignalBindingRateLimit:           {target: ClassQuota},
	SignalPoolCapacityExhausted:      {target: ClassQuota},
	SignalAllChannelsDegraded:        {target: ClassQuota},
	SignalQueueWaitTimeout:           {target: ClassQuota},
	SignalQueueWaitOverflow:          {target: ClassQuota},
	SignalUpstreamRateLimit:          {target: ClassQuota, requiresRetryPermission: true},
	SignalLocalContextWindow:         {target: ClassContextWindow},
	SignalUpstreamContextWindow:      {target: ClassContextWindow},
	SignalUpstreamContentPolicy:      {target: ClassSafety, requiresLocalSafetyPass: true},
	SignalUpstreamServerError:        {target: ClassManual, requiresRetryPermission: true},
	SignalTransientConnectionFailure: {target: ClassManual, requiresRetryPermission: true},
	SignalConnectTimeout:             {target: ClassManual, requiresRetryPermission: true},
	SignalFirstByteTimeout:           {target: ClassManual, requiresRetryPermission: true},
	SignalUpstreamTimeout:            {target: ClassManual, requiresRetryPermission: true},
	SignalEmptyResponse:              {target: ClassManual, requiresRetryPermission: true},
}

// TransitionState 是单模型、单请求的一次跨类终态门输入。
type TransitionState struct {
	CurrentClass      Class
	PrimaryExhausted  bool
	TargetConfigured  bool
	RetryPermitted    bool
	LocalSafetyPassed bool
	DeliveryStarted   bool
	TransitionUsed    bool
}

// TargetClass 返回规范化信号唯一对应的目标类别。
func TargetClass(signal Signal) (Class, bool) {
	rule, ok := triggerRules[signal]
	if !ok {
		return "", false
	}
	return rule.target, true
}

// IsTerminal 报告信号是否必须原地终止。未知信号同样 fail-closed。
func IsTerminal(signal Signal) bool {
	_, ok := triggerRules[signal]
	return !ok
}

// AllowTransition 同时执行触发归约与终态门。只有尚未交付、主类预算已耗尽、
// 目标已配置且本请求尚未跨类时，才允许从 normal 进入一个精确目标类别。
func AllowTransition(signal Signal, state TransitionState) (Class, bool) {
	rule, ok := triggerRules[signal]
	if !ok {
		return "", false
	}
	if NormalizeClass(string(state.CurrentClass)) != ClassNormal ||
		!state.PrimaryExhausted ||
		!state.TargetConfigured ||
		state.DeliveryStarted ||
		state.TransitionUsed {
		return "", false
	}
	if rule.requiresRetryPermission && !state.RetryPermitted {
		return "", false
	}
	if rule.requiresLocalSafetyPass && !state.LocalSafetyPassed {
		return "", false
	}
	return rule.target, true
}
