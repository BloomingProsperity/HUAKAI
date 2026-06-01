package clienterr

// Public error catalog.
//
// code | HTTP status | fixed message | covering test
// registry_unknown_error | 500 | model registry failed | TestMessageForKnownCodesAndFallback
// router_plan_error | 500 | route planning failed | TestMessageForKnownCodesAndFallback
// pricing_unavailable | 503 | pricing is temporarily unavailable | TestMessageForKnownCodesAndFallback
// reserve_error | 500 | request reservation failed | TestMessageForKnownCodesAndFallback
// insufficient_balance | 402 | 余额不足 | TestMessageForKnownCodesAndFallback
// no_capacity | 503 | no capacity is currently available | TestMessageForKnownCodesAndFallback
// claim_race | 409 | request reservation changed; retry the request | TestPR5ClaimRaceAbortFailureSurfacesSafeHeader
// pool_select_error | 500 | account selection failed | TestMessageForKnownCodesAndFallback
// credential_resolve_error | 500/503 | upstream credential unavailable | TestMessageForKnownCodesAndFallback
// queue_wait | 429 | request is queued; retry later | TestHandler_WaitPlanReturnsQueueWait
// invalid_request_body | 400 | request body is invalid | TestMessageForKnownCodesAndFallback
// non_streaming_not_yet_wired | 503 | non-streaming dispatch is unavailable | TestMessageForKnownCodesAndFallback
// upstream_dispatch_error | varies | upstream request failed | TestHandler_HCSFUpstreamHTTPErrorDoesNotLeakBody
// upstream_empty_response | 502 | upstream returned no response body | TestMessageForKnownCodesAndFallback
// upstream_read_error | 502 | upstream response could not be read | TestMessageForKnownCodesAndFallback
// upstream_response_too_large | 502 | upstream response is too large | TestHandler_RawBufferedBodyOverLimitIsTypedError
// upstream_adapter_error | 502 | upstream adapter failed | TestMessageForKnownCodesAndFallback
// canonical_response_error | 502 | upstream response could not be converted | TestMessageForKnownCodesAndFallback
// cache_key_error | 400 | request cache key could not be built | TestMessageForKnownCodesAndFallback
// audit_ledger_error | 500 | audit ledger failed | TestMessageForKnownCodesAndFallback
// audit_ref_missing | 500 | Audit reference missing for money-path operation. | TestMessageForKnownCodesAndFallback
// settle_error | 500 | request settlement failed | TestStreamingForwardSettleAndAbortErrorsAreLoggedNotHeaders
// cache_settle_error | 500 | cache hit settlement failed | TestMessageForKnownCodesAndFallback
// streaming_adapter_unregistered | 503 | streaming adapter is unavailable | TestMessageForKnownCodesAndFallback
// streaming_translation_not_supported | 501 | streaming translation is not supported | TestMessageForKnownCodesAndFallback
// stream_forward_error | 502/503 | upstream stream failed before delivery | TestStreamingIdempotencyReplayAbortsZeroByteForwardError
// invalid_json | 400 | request body is not valid JSON | TestValidateChatCompletionsRequestInvalidJSONUsesFixedMessage
// body_read_error | 400 | request body could not be read | TestReadChatRequestBodyErrorDoesNotLeakReaderError
// replay_lookup_failed | 503 | idempotency replay is unavailable | TestMessageForKnownCodesAndFallback
// attempt_failed | 502 | request attempt failed | TestMessageForKnownCodesAndFallback
// abort_failed | header | internal settlement failed | TestDegradeFailureIfAbortFailedUsesSafeAbortReasonAndLogsRawError
// forward_failed | header | stream forwarding failed | TestStreamingForwardSettleAndAbortErrorsAreLoggedNotHeaders
// settle_failed | header | request settlement failed | TestStreamingForwardSettleAndAbortErrorsAreLoggedNotHeaders
const (
	CodeRegistryUnknownError            = "registry_unknown_error"
	CodeRouterPlanError                 = "router_plan_error"
	CodePricingUnavailable              = "pricing_unavailable"
	CodeReserveError                    = "reserve_error"
	CodeInsufficientBalance             = "insufficient_balance"
	CodeNoCapacity                      = "no_capacity"
	CodeClaimRace                       = "claim_race"
	CodePoolSelectError                 = "pool_select_error"
	CodeCredentialResolveError          = "credential_resolve_error"
	CodeQueueWait                       = "queue_wait"
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
	CodeCredentialResolveError:          "upstream credential unavailable",
	CodeQueueWait:                       "request is queued; retry later",
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
