package gatewayhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const (
	headerHuakaiAbortFailed  = "X-Huakai-Abort-Failed"
	headerHuakaiForwardError = "X-Huakai-Forward-Error"
	headerHuakaiSettleError  = "X-Huakai-Settle-Error"
)

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, code, message)
}

func writeLoggedJSONError(ctx context.Context, requestID string, w http.ResponseWriter, status int, code string, err error) {
	logInternalError(ctx, requestID, code, err)
	writeJSONError(w, status, code, clienterr.MessageFor(code))
}

func logInternalError(ctx context.Context, requestID, code string, err error) {
	clienterr.LogInternal(ctx, requestID, code, err)
}

func setAbortFailedHeader(w http.ResponseWriter, ctx context.Context, requestID string, err error) {
	logInternalError(ctx, requestID, clienterr.CodeAbortFailed, err)
	if w != nil {
		w.Header().Set(headerHuakaiAbortFailed, clienterr.CodeAbortFailed)
	}
}

func writeNormalizedUpstreamError(w http.ResponseWriter, status int, fallbackCode string, c gateway.Classification) {
	code := fallbackCode
	if c.Class != "" {
		code = "upstream_" + string(c.Class)
	}
	if c.RetryAfterMs > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", (c.RetryAfterMs+999)/1000))
	}
	writeJSONError(w, status, code, "upstream request failed")
}

func clientStatusForUpstreamError(upstreamStatus int, class gateway.ErrorClass) int {
	switch class {
	case gateway.ErrorClassRequestTooLarge:
		return http.StatusBadRequest
	case gateway.ErrorClassRateLimited, gateway.ErrorClassOverloaded, gateway.ErrorClassUpstreamTimeout:
		return http.StatusServiceUnavailable
	}
	if upstreamStatus == http.StatusTooManyRequests {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

func closeDispatchResult(res *gateway.DispatchResult) {
	if res != nil && res.Close != nil {
		_ = res.Close()
	}
}

func channelHealthKey(tenantID int64, account provider.AccountInfo) (channelhealth.ChannelKey, bool) {
	key := channelhealth.ChannelKey{
		TenantID:            tenantID,
		Vendor:              account.Platform,
		ProviderAccountID:   account.AccountID,
		AccountCredentialID: account.AccountCredentialID,
		CredentialVersion:   account.CredentialVersion,
	}
	if err := key.Validate(); err != nil {
		return channelhealth.ChannelKey{}, false
	}
	key.ChannelID = key.StableChannelID()
	return key, true
}

func recordChannelHealthSignal(ctx context.Context, d ChatHandlerDeps, key channelhealth.ChannelKey, class channelhealth.SignalClass, statusCode int, latency time.Duration, requestID string, resetAt *time.Time) {
	if d.ChannelHealth == nil || class == "" {
		return
	}
	latencyMS := latency.Milliseconds()
	if latencyMS < 0 {
		latencyMS = 0
	}
	_, _ = d.ChannelHealth.ApplySignal(ctx, channelhealth.Signal{
		Key:              key,
		Class:            class,
		StatusCode:       statusCode,
		LatencyMS:        latencyMS,
		RequestID:        requestID,
		RateLimitResetAt: resetAt,
	})
}

func signalFromClassification(statusCode int, c gateway.Classification) channelhealth.SignalClass {
	switch c.Class {
	case gateway.ErrorClassRateLimited:
		return channelhealth.SignalRateLimit
	case gateway.ErrorClassServerError, gateway.ErrorClassOverloaded:
		return channelhealth.SignalUpstream5xx
	case gateway.ErrorClassNetworkTimeout, gateway.ErrorClassUpstreamTimeout:
		return channelhealth.SignalTimeout
	case gateway.ErrorClassTokenRevoked, gateway.ErrorClassOAuthInvalidGrant:
		// Phase 1 综合稿 override-1: 401 触发一次 auth failover/refresh intent，
		// 但不把令牌问题写成账号健康降级信号。
		return ""
	case gateway.ErrorClassKYCRequired, gateway.ErrorClassOrgDisabled,
		gateway.ErrorClassWorkspaceDeactivated, gateway.ErrorClassCreditExhausted:
		return channelhealth.SignalAccountSuspended
	case gateway.ErrorClassPlatformPolicy:
		return channelhealth.SignalForbidden
	}
	switch {
	case statusCode == http.StatusTooManyRequests:
		return channelhealth.SignalRateLimit
	case statusCode == http.StatusForbidden:
		return channelhealth.SignalForbidden
	case statusCode >= 500:
		return channelhealth.SignalUpstream5xx
	default:
		return channelhealth.SignalChannelError
	}
}

func signalFromDispatchError(err error, c gateway.Classification) channelhealth.SignalClass {
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return channelhealth.SignalTimeout
	}
	return signalFromClassification(0, c)
}

func rateLimitResetFromClassification(c gateway.Classification, now time.Time) *time.Time {
	if c.RetryAfterMs <= 0 {
		return nil
	}
	reset := now.Add(time.Duration(c.RetryAfterMs) * time.Millisecond)
	return &reset
}
