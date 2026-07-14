// Package executor 把网关、pool 的规范化失败投影成绑定级降级信号。
// 它只做稳定分类与 HTTP 失败描述，不执行 IO、reserve、abort 或 settle。
package executor

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

// SignalFromDecision 把已规范化的上游/传输失败映射到唯一绑定降级信号。
func SignalFromDecision(classification gateway.Classification, decision gateway.AttemptRetryDecision) bindingfallback.Signal {
	switch classification.Class {
	case gateway.ErrorClassRateLimited:
		return bindingfallback.SignalUpstreamRateLimit
	case gateway.ErrorClassServerError, gateway.ErrorClassOverloaded:
		return bindingfallback.SignalUpstreamServerError
	case gateway.ErrorClassNetworkTimeout:
		return bindingfallback.SignalTransientConnectionFailure
	case gateway.ErrorClassUpstreamTimeout:
		return bindingfallback.SignalUpstreamTimeout
	case gateway.ErrorClassOAuthInvalidGrant, gateway.ErrorClassTokenRevoked,
		gateway.ErrorClassKYCRequired, gateway.ErrorClassOrgDisabled,
		gateway.ErrorClassWorkspaceDeactivated, gateway.ErrorClassCreditExhausted:
		return bindingfallback.SignalUpstreamAuthFailure
	case gateway.ErrorClassPlatformPolicy:
		return bindingfallback.SignalPermissionDenied
	case gateway.ErrorClassRequestTooLarge:
		return bindingfallback.SignalRequestBodyTooLarge
	}

	switch decision.TransportClass {
	case gateway.TransportErrorConnectTimeout:
		return bindingfallback.SignalConnectTimeout
	case gateway.TransportErrorNetworkTimeout:
		return bindingfallback.SignalTransientConnectionFailure
	case gateway.TransportErrorUpstreamHeaderTimeout:
		return bindingfallback.SignalFirstByteTimeout
	case gateway.TransportErrorUpstreamBodyIdleTimeout:
		return bindingfallback.SignalUpstreamTimeout
	case gateway.TransportErrorConnectionRefused, gateway.TransportErrorDNSFailure,
		gateway.TransportErrorNetworkUnreachable, gateway.TransportErrorProxyFailure:
		return bindingfallback.SignalTransientConnectionFailure
	case gateway.TransportErrorCredentialExpired:
		return bindingfallback.SignalUpstreamAuthFailure
	case gateway.TransportErrorTLSHandshakeFailed, gateway.TransportErrorLocalDispatch:
		return bindingfallback.SignalLocalConfigurationFailure
	}

	switch decision.AbortReason {
	case "binding_concurrency_limited":
		return bindingfallback.SignalBindingConcurrencyLimit
	case "binding_rate_limited":
		return bindingfallback.SignalBindingRateLimit
	case "key_rate_limited":
		return bindingfallback.SignalKeyRateLimit
	case "pool_no_capacity":
		return bindingfallback.SignalPoolCapacityExhausted
	case "queue_wait":
		return bindingfallback.SignalQueueWaitTimeout
	case "queue_wait_cancelled":
		return bindingfallback.SignalQueueWaitCancelled
	case "upstream_rate_limited":
		return bindingfallback.SignalUpstreamRateLimit
	case "upstream_5xx", "upstream_overloaded":
		return bindingfallback.SignalUpstreamServerError
	case "upstream_timeout":
		return bindingfallback.SignalUpstreamTimeout
	case "upstream_empty_response":
		return bindingfallback.SignalEmptyResponse
	case "upstream_auth_failure", "local_credential_expired", "credential_protocol_incompatible":
		return bindingfallback.SignalUpstreamAuthFailure
	case "credential_resolve_error":
		return bindingfallback.SignalCredentialResolutionFailure
	case "claim_race":
		return bindingfallback.SignalClaimConflict
	case "request_too_large":
		return bindingfallback.SignalRequestBodyTooLarge
	case "upstream_forbidden":
		return bindingfallback.SignalPermissionDenied
	case "upstream_client_4xx":
		return bindingfallback.SignalRequestMalformed
	default:
		return bindingfallback.SignalLocalConfigurationFailure
	}
}

// SignalFromUpstream 只读取 JSON 机器枚举。普通 400/403/413 不能凭状态码
// 触发 context_window 或 safety。
func SignalFromUpstream(status int, body []byte, classification gateway.Classification, decision gateway.AttemptRetryDecision) bindingfallback.Signal {
	tokens := upstreamMachineTokens(body)
	if status == http.StatusBadRequest || status == http.StatusRequestEntityTooLarge || status == http.StatusUnprocessableEntity {
		if containsToken(tokens, "context_length_exceeded", "context_window_exceeded", "max_context_length_exceeded", "input_too_long", "too_many_tokens") {
			return bindingfallback.SignalUpstreamContextWindow
		}
	}
	if status == http.StatusBadRequest || status == http.StatusForbidden {
		if containsToken(tokens, "content_policy_violation", "content_filter", "content_filtered", "safety_violation", "blocked_by_safety") {
			return bindingfallback.SignalUpstreamContentPolicy
		}
	}
	return SignalFromDecision(classification, decision)
}

// SignalFromPoolError 保留 selector 的结构化耗尽族，避免把静态不匹配伪装成容量不足。
func SignalFromPoolError(err error) bindingfallback.Signal {
	var noCapacity *pool.NoCapacityError
	if errors.As(err, &noCapacity) && noCapacity != nil {
		switch {
		case noCapacity.ContextWindowOnly():
			return bindingfallback.SignalLocalContextWindow
		case noCapacity.PureCapacity():
			if errors.Is(err, pool.ErrAllChannelsDegraded) {
				return bindingfallback.SignalAllChannelsDegraded
			}
			return bindingfallback.SignalPoolCapacityExhausted
		default:
			return bindingfallback.SignalPoolStaticMismatch
		}
	}
	if errors.Is(err, pool.ErrAllChannelsDegraded) {
		return bindingfallback.SignalAllChannelsDegraded
	}
	if errors.Is(err, pool.ErrNoSlotAvailable) {
		return bindingfallback.SignalPoolCapacityExhausted
	}
	return bindingfallback.SignalPoolStaticMismatch
}

func upstreamMachineTokens(body []byte) map[string]struct{} {
	if len(body) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil
	}
	tokens := make(map[string]struct{})
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				switch strings.ToLower(strings.TrimSpace(key)) {
				case "code", "type", "reason", "finish_reason", "block_reason":
					if raw, ok := child.(string); ok {
						if token := strings.ToLower(strings.TrimSpace(raw)); token != "" {
							tokens[token] = struct{}{}
						}
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return tokens
}

func containsToken(tokens map[string]struct{}, wanted ...string) bool {
	for _, token := range wanted {
		if _, ok := tokens[token]; ok {
			return true
		}
	}
	return false
}
