package clienterr

// 对外错误目录。
//
// 错误码 | HTTP 状态 | 固定消息 | 覆盖测试
// registry_unknown_error | 500 | 模型目录失败 | TestMessageForKnownCodesAndFallback
// router_plan_error | 500 | 路由规划失败 | TestMessageForKnownCodesAndFallback
// pricing_unavailable | 503 | 计价暂不可用 | TestMessageForKnownCodesAndFallback
// reserve_error | 500 | 请求预留失败 | TestMessageForKnownCodesAndFallback
// insufficient_balance | 402 | 余额不足 | TestMessageForKnownCodesAndFallback
// no_capacity | 503 | 当前无可用容量 | TestMessageForKnownCodesAndFallback
// claim_race | 409 | 请求预留已变化;请重试请求 | TestPR5ClaimRaceAbortFailureSurfacesSafeHeader
// pool_select_error | 500 | 账号选择失败 | TestMessageForKnownCodesAndFallback
// group_policy_unavailable | 503 | 分组路由策略暂不可用 | TestMessageForKnownCodesAndFallback
// credential_resolve_error | 500/503 | 上游凭据不可用 | TestMessageForKnownCodesAndFallback
// queue_wait | 429 | 请求已排队;请稍后重试 | TestHandler_WaitPlanReturnsQueueWait
// invalid_request_body | 400 | 请求体无效 | TestMessageForKnownCodesAndFallback
// non_streaming_not_yet_wired | 503 | 非流式调度不可用 | TestMessageForKnownCodesAndFallback
// upstream_dispatch_error | varies | 上游请求失败 | TestHandler_HCSFUpstreamHTTPErrorDoesNotLeakBody
// upstream_empty_response | 502 | 上游未返回响应体 | TestMessageForKnownCodesAndFallback
// upstream_read_error | 502 | 无法读取上游响应 | TestMessageForKnownCodesAndFallback
// upstream_response_too_large | 502 | 上游响应过大 | TestHandler_RawBufferedBodyOverLimitIsTypedError
// upstream_adapter_error | 502 | 上游适配器失败 | TestMessageForKnownCodesAndFallback
// canonical_response_error | 502 | 无法转换上游响应 | TestMessageForKnownCodesAndFallback
// cache_key_error | 400 | 无法构造请求缓存键 | TestMessageForKnownCodesAndFallback
// audit_ledger_error | 500 | 审计账本失败 | TestMessageForKnownCodesAndFallback
// audit_ref_missing | 500 | 资金路径操作缺少审计引用 | TestMessageForKnownCodesAndFallback
// settle_error | 500 | 请求结算失败 | TestStreamingForwardSettleAndAbortErrorsAreLoggedNotHeaders
// cache_settle_error | 500 | 缓存命中结算失败 | TestMessageForKnownCodesAndFallback
// streaming_adapter_unregistered | 503 | 流式适配器不可用 | TestMessageForKnownCodesAndFallback
// streaming_translation_not_supported | 501 | 不支持流式翻译 | TestMessageForKnownCodesAndFallback
// stream_forward_error | 502/503 | 上游流在交付前失败 | TestStreamingIdempotencyReplayAbortsZeroByteForwardError
// invalid_json | 400 | 请求体不是合法 JSON | TestValidateChatCompletionsRequestInvalidJSONUsesFixedMessage
// body_read_error | 400 | 无法读取请求体 | TestReadChatRequestBodyErrorDoesNotLeakReaderError
// replay_lookup_failed | 503 | 幂等回放不可用 | TestMessageForKnownCodesAndFallback
// attempt_failed | 502 | 请求尝试失败 | TestMessageForKnownCodesAndFallback
// content_policy_violation | 403 | 请求违反内容政策 | TestMessageForKnownCodesAndFallback
// official_client_required | 403 | 账号要求官方客户端 | TestMessageForKnownCodesAndFallback
// abort_failed | header | 内部结算失败 | TestDegradeFailureIfAbortFailedUsesSafeAbortReasonAndLogsRawError
// forward_failed | header | 流转发失败 | TestStreamingForwardSettleAndAbortErrorsAreLoggedNotHeaders
// settle_failed | header | 请求结算失败 | TestStreamingForwardSettleAndAbortErrorsAreLoggedNotHeaders
const (
	CodeRegistryUnknownError            = "registry_unknown_error"
	CodeRouterPlanError                 = "router_plan_error"
	CodePricingUnavailable              = "pricing_unavailable"
	CodeReserveError                    = "reserve_error"
	CodeInsufficientBalance             = "insufficient_balance"
	CodeNoCapacity                      = "no_capacity"
	CodeClaimRace                       = "claim_race"
	CodePoolSelectError                 = "pool_select_error"
	CodeGroupPolicyUnavailable          = "group_policy_unavailable"
	CodeCredentialResolveError          = "credential_resolve_error"
	CodeQueueWait                       = "queue_wait"
	CodeKeyRateLimited                  = "rate_limit_exceeded"
	CodeBindingConcurrencyLimited       = "binding_concurrency_limit_exceeded"
	CodeInvalidRequestBody              = "invalid_request_body"
	CodeNonStreamingNotYetWired         = "non_streaming_not_yet_wired"
	CodeUpstreamDispatchError           = "upstream_dispatch_error"
	CodeUpstreamEmptyResponse           = "upstream_empty_response"
	CodeUpstreamReadError               = "upstream_read_error"
	CodeUpstreamResponseTooLarge        = "upstream_response_too_large"
	CodeUpstreamAdapterError            = "upstream_adapter_error"
	CodeCanonicalResponseError          = "canonical_response_error"
	CodeCacheKeyError                   = "cache_key_error"
	CodeAuditLedgerError                = "audit_ledger_error"
	CodeAuditRefMissing                 = "audit_ref_missing"
	CodeSettleError                     = "settle_error"
	CodeCacheSettleError                = "cache_settle_error"
	CodeStreamingAdapterUnregistered    = "streaming_adapter_unregistered"
	CodeStreamingTranslationUnsupported = "streaming_translation_not_supported"
	CodeStreamForwardError              = "stream_forward_error"
	CodeInvalidJSON                     = "invalid_json"
	CodeBodyReadError                   = "body_read_error"
	CodeReplayLookupFailed              = "replay_lookup_failed"
	CodeAttemptFailed                   = "attempt_failed"
	CodeContentPolicyViolation          = "content_policy_violation"
	CodeOfficialClientRequired          = "official_client_required"
	CodeAbortFailed                     = "abort_failed"
	CodeForwardFailed                   = "forward_failed"
	CodeSettleFailed                    = "settle_failed"
)

var messages = map[string]string{
	CodeRegistryUnknownError:            "model registry failed",
	CodeRouterPlanError:                 "route planning failed",
	CodePricingUnavailable:              "pricing is temporarily unavailable",
	CodeReserveError:                    "request reservation failed",
	CodeInsufficientBalance:             "余额不足",
	CodeNoCapacity:                      "no capacity is currently available",
	CodeClaimRace:                       "request reservation changed; retry the request",
	CodePoolSelectError:                 "account selection failed",
	CodeGroupPolicyUnavailable:          "group routing policy is temporarily unavailable",
	CodeCredentialResolveError:          "upstream credential unavailable",
	CodeQueueWait:                       "request is queued; retry later",
	CodeKeyRateLimited:                  "API key rate limit exceeded; retry later",
	CodeBindingConcurrencyLimited:       "binding concurrency limit exceeded; retry later",
	CodeInvalidRequestBody:              "request body is invalid",
	CodeNonStreamingNotYetWired:         "non-streaming dispatch is unavailable",
	CodeUpstreamDispatchError:           "upstream request failed",
	CodeUpstreamEmptyResponse:           "upstream returned no response body",
	CodeUpstreamReadError:               "upstream response could not be read",
	CodeUpstreamResponseTooLarge:        "upstream response is too large",
	CodeUpstreamAdapterError:            "upstream adapter failed",
	CodeCanonicalResponseError:          "upstream response could not be converted",
	CodeCacheKeyError:                   "request cache key could not be built",
	CodeAuditLedgerError:                "audit ledger failed",
	CodeAuditRefMissing:                 "Audit reference missing for money-path operation.",
	CodeSettleError:                     "request settlement failed",
	CodeCacheSettleError:                "cache hit settlement failed",
	CodeStreamingAdapterUnregistered:    "streaming adapter is unavailable",
	CodeStreamingTranslationUnsupported: "streaming translation is not supported",
	CodeStreamForwardError:              "upstream stream failed before delivery",
	CodeInvalidJSON:                     "request body is not valid JSON",
	CodeBodyReadError:                   "request body could not be read",
	CodeReplayLookupFailed:              "idempotency replay is unavailable",
	CodeAttemptFailed:                   "request attempt failed",
	CodeContentPolicyViolation:          "request violates content policy",
	CodeOfficialClientRequired:          "account requires its official client",
	CodeAbortFailed:                     "internal settlement failed",
	CodeForwardFailed:                   "stream forwarding failed",
	CodeSettleFailed:                    "request settlement failed",
}

func MessageFor(code string) string {
	if message, ok := messages[code]; ok {
		return message
	}
	return "request failed"
}
