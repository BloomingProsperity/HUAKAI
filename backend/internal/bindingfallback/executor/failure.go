package executor

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

// Failure 是各协议 attempt 在尚未写客户端时返回给共享状态机的稳定失败。
type Failure struct {
	Signal            bindingfallback.Signal
	RetryPermitted    bool
	Status            int
	Code              string
	Message           string
	AbortReason       string
	RetryAfterSeconds int
	// AuthFailoverEligible 标记上游授权失败(401/凭据错配):可消费整请求一次的
	// 授权换号子预算(独立于普通 attempt 预算)。该信号对跨类终态门 fail-closed,
	// 换号与否由各协议 run 循环按子预算裁决。
	AuthFailoverEligible bool
}

// PoolFailure 归一化 selector 失败；调用方仍负责先 abort/release。
func PoolFailure(err error) *Failure {
	switch {
	case errors.Is(err, pool.ErrBindingConcurrencyLimited):
		return newFailure(bindingfallback.SignalBindingConcurrencyLimit, false, http.StatusTooManyRequests,
			clienterr.CodeBindingConcurrencyLimited, "binding_concurrency_limited", 1)
	case errors.Is(err, pool.ErrBindingRateLimited):
		return newFailure(bindingfallback.SignalBindingRateLimit, false, http.StatusTooManyRequests,
			clienterr.CodeKeyRateLimited, "binding_rate_limited", 1)
	case errors.Is(err, pool.ErrKeyRateLimited):
		return newFailure(bindingfallback.SignalKeyRateLimit, false, http.StatusTooManyRequests,
			clienterr.CodeKeyRateLimited, "key_rate_limited", 1)
	case errors.Is(err, pool.ErrNoEligibleAccount), errors.Is(err, pool.ErrNoSlotAvailable), errors.Is(err, pool.ErrAllChannelsDegraded):
		return newFailure(SignalFromPoolError(err), true, http.StatusServiceUnavailable,
			clienterr.CodeNoCapacity, "pool_no_capacity", 0)
	case errors.Is(err, pool.ErrClaimRace):
		return newFailure(bindingfallback.SignalClaimConflict, false, http.StatusConflict,
			clienterr.CodeClaimRace, "claim_race", 1)
	default:
		return newFailure(bindingfallback.SignalLocalConfigurationFailure, false, http.StatusInternalServerError,
			clienterr.CodePoolSelectError, "pool_select_error", 0)
	}
}

// DispatchFailure 归一化尚未取得上游响应的传输失败。
func DispatchFailure(err error) *Failure {
	decision := gateway.ClassifyAttemptDispatchError(err)
	failure := newFailure(SignalFromDecision(gateway.Classification{}, decision), decision.RetryableBeforeDelivery,
		http.StatusBadGateway, clienterr.CodeUpstreamDispatchError, "upstream_dispatch_error", 0)
	failure.AuthFailoverEligible = decision.CountsAgainstAuthFailoverBudget
	return failure
}

// EmptyResponseFailure 表示上游没有返回可读取响应，允许 manual 目标接管。
func EmptyResponseFailure() *Failure {
	return newFailure(bindingfallback.SignalEmptyResponse, true, http.StatusBadGateway,
		clienterr.CodeUpstreamEmptyResponse, "upstream_empty_response", 0)
}

// ReadFailure 只在读取尚未交付的上游响应失败时使用。
func ReadFailure(err error) *Failure {
	decision := gateway.ClassifyAttemptDispatchError(err)
	return newFailure(SignalFromDecision(gateway.Classification{}, decision), decision.RetryableBeforeDelivery,
		http.StatusBadGateway, clienterr.CodeUpstreamReadError, "upstream_read_error", 0)
}

// UpstreamFailure 归一化非 2xx 响应，并保留窄机器码触发边界。
// 客户端状态用分类器 ClientStatus:客户端错误(400/403 等)原样透传而非折叠成 502,
// 5xx→502、限流/过载→503、401→401,与流式主路对齐。
func UpstreamFailure(status int, headers http.Header, body []byte, providerName string) *Failure {
	decision, classification, err := gateway.ClassifyAttemptHTTPError(status, headers, body, providerName)
	if err != nil {
		return newFailure(bindingfallback.SignalLocalConfigurationFailure, false, http.StatusBadGateway,
			clienterr.CodeUpstreamDispatchError, "upstream_error", 0)
	}
	reason := decision.AbortReason
	if reason == "" {
		reason = "upstream_error"
	}
	clientStatus := decision.ClientStatus
	if clientStatus <= 0 {
		clientStatus = http.StatusBadGateway
	}
	failure := newFailure(SignalFromUpstream(status, body, classification, decision), decision.RetryableBeforeDelivery,
		clientStatus, clienterr.CodeUpstreamDispatchError, reason, 0)
	failure.AuthFailoverEligible = decision.CountsAgainstAuthFailoverBudget
	return failure
}

// CredentialCompatibilityFailure 表示发网前凭据形态与协议族静态不匹配(如反转号
// 打 api-key 直连):本号必然失败但池子未必坏,换号重试有意义;与上游 401 共用
// 授权换号子预算,防错配号连环烧穿 attempt 预算。
func CredentialCompatibilityFailure() *Failure {
	decision := gateway.CredentialProtocolIncompatibleDecision()
	failure := newFailure(bindingfallback.SignalUpstreamAuthFailure, decision.RetryableBeforeDelivery,
		decision.ClientStatus, clienterr.CodeCredentialResolveError, decision.AbortReason, 0)
	failure.AuthFailoverEligible = decision.CountsAgainstAuthFailoverBudget
	return failure
}

// AbortFailure 阻止 abort/release 失败后继续 reserve 另一个 attempt。
func AbortFailure() *Failure {
	return newFailure(bindingfallback.SignalBillingAbortFailure, false, http.StatusInternalServerError,
		clienterr.CodeAbortFailed, "abort_failed", 0)
}

func newFailure(signal bindingfallback.Signal, retry bool, status int, code, reason string, retryAfter int) *Failure {
	return &Failure{
		Signal: signal, RetryPermitted: retry, Status: status, Code: code,
		Message: clienterr.MessageFor(code), AbortReason: reason, RetryAfterSeconds: retryAfter,
	}
}

// WriteHTTP 在状态机决定终止后才写统一 JSON 错误。
func WriteHTTP(w http.ResponseWriter, failure *Failure) {
	if w == nil || failure == nil {
		return
	}
	if failure.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(failure.RetryAfterSeconds))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(failure.Status)
	body, err := json.Marshal(map[string]map[string]string{
		"error": {"code": failure.Code, "message": failure.Message},
	})
	if err != nil {
		body = []byte(`{"error":{"code":"internal_error","message":"internal error"}}`)
	}
	_, _ = w.Write(body)
}
