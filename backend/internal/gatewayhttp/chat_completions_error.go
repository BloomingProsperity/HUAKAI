package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

const (
	headerHuakaiAbortFailed  = "X-Huakai-Abort-Failed"
	headerHuakaiForwardError = "X-Huakai-Forward-Error"
	headerHuakaiSettleError  = "X-Huakai-Settle-Error"
)

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encodeJSONErrorBody(code, message))
}

func writeInsufficientBalanceError(w http.ResponseWriter) {
	writeInsufficientQuotaBody(w, http.StatusPaymentRequired, 0, "")
}

func writeInsufficientQuotaError(w http.ResponseWriter) {
	writeInsufficientQuotaBody(w, http.StatusTooManyRequests, 0, "")
}

// writeInsufficientQuotaErrorRetryable 在 token-per-window 等窗口配额拒绝时,把引擎算好的窗口信息
// 吐给客户端:Retry-After 头(秒)+ body 的 window_resets_at(RFC3339);windowKind 非空时再加 body 的
// quota_window(如 calendar_month),让 SDK 既能按窗口边界退避,又能区分是日额还是月额超了。
func writeInsufficientQuotaErrorRetryable(w http.ResponseWriter, retryAfter time.Duration, windowKind string) {
	writeInsufficientQuotaBody(w, http.StatusTooManyRequests, retryAfter, windowKind)
}

func writeInsufficientQuotaBody(w http.ResponseWriter, status int, retryAfter time.Duration, windowKind string) {
	w.Header().Set("Content-Type", "application/json")
	errFields := map[string]string{
		"type":    "insufficient_quota",
		"code":    clienterr.CodeInsufficientBalance,
		"message": clienterr.MessageFor(clienterr.CodeInsufficientBalance),
	}
	if retryAfter > 0 {
		secs := int64(math.Ceil(retryAfter.Seconds()))
		w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
		errFields["window_resets_at"] = time.Now().UTC().Add(retryAfter).Format(time.RFC3339)
	}
	// 窗口种类与 window_resets_at 解耦:manual/none 窗口无固定重置(retryAfter=0、无 resets_at),但仍
	// 可透出窗口名;空串(未配多窗口/未知)则完全不写本字段,对既有客户端零变化。
	if windowKind != "" {
		errFields["quota_window"] = windowKind
	}
	w.WriteHeader(status)
	body, err := json.Marshal(map[string]map[string]string{"error": errFields})
	if err != nil {
		body = []byte(`{"error":{"type":"insufficient_quota","code":"insufficient_balance","message":"余额不足"}}`)
	}
	_, _ = w.Write(body)
}

// encodeJSONErrorBody 用 encoding/json 编码 {"error":{"code","message"}},而非 fmt %q 手拼。
// %q 是 Go 字符串字面量格式化器:对部分控制字节(如 \x01)会输出 \xNN —— 合法 Go 字面量却是
// 非法 JSON,严格客户端/SDK/反代日志解析会失败。code 多为内部常量,但 message 可能携带
// 用户可控内容(如 admin 创建账号时回显归一化后的 vendor)。json.Marshal 对 string 不会失败,
// 兜底仍回退一个静态合法 JSON,绝不写出半截/非法响应。
func encodeJSONErrorBody(code, message string) []byte {
	body, err := json.Marshal(map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
	if err != nil {
		return []byte(`{"error":{"code":"internal_error","message":"internal error"}}`)
	}
	return body
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

// recordChannelHealthSignal 记一条渠道健康信号。authClass 仅当 class==SignalAuthChallenge 时有意义
// (auth 车道 iron-clad/ambiguous 分级);非 auth 信号传 0(ambiguous,不被消费)。SignalAuthChallenge
// 刻意不喂 RecentReqRing——auth blip 既不进健康 FSM 也不污染 RPM 累计,与接线前(auth 返回空信号、
// 直接跳过)的 RPM 行为逐字节等价。
func recordChannelHealthSignal(ctx context.Context, d ChatHandlerDeps, key channelhealth.ChannelKey, class channelhealth.SignalClass, statusCode int, latency time.Duration, requestID string, resetAt *time.Time, authClass authcooldown.FailureClass) {
	if d.RecentReqRing != nil && key.ProviderAccountID != 0 && class != "" && class != channelhealth.SignalAuthChallenge {
		d.RecentReqRing.Record(key.ProviderAccountID, class == channelhealth.SignalSuccess)
	}
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
		AuthFailureClass: authClass,
	})
}

// triggerCredentialHotRefresh 在 401 时异步跑凭证热刷新,并把结果单向通报选号 auth 车道:
// 拿到 invalid_grant→即时升 HardDisabled(authLane 为 nil 时空操作)。刷新「成功」刻意不解除
// 冷却——RefreshHotPath 返回 nil 不代表真的刷新了(去抖窗口跳过/storm 预算拒绝/静态 API-key
// 无可刷新都返回 nil),车道侧对 success 一律不动状态(见 authcooldown.OnRefreshResult)。
func (ex *chatExecution) triggerCredentialHotRefresh(accountID int64) {
	if ex == nil || ex.d.CredentialHotRefresher == nil || accountID == 0 {
		return
	}
	tenantID := ex.ident.TenantID
	vendor := ex.accInfo.Platform
	if vendor == "" {
		vendor = pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily)
	}
	requestID := ex.requestID
	refresher := ex.d.CredentialHotRefresher
	authLane := ex.d.AuthCooldown
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), credentialHotRefreshTimeout)
		defer cancel()
		err := refresher.RefreshHotPath(ctx, tenantID, accountID, vendor)
		authLane.OnRefreshResult(ctx, accountID, err == nil, err != nil && authcooldown.IsPermanentRefreshError(err))
		if err != nil {
			logInternalError(ctx, requestID, "credential_hot_refresh_failed", err)
		}
	}()
}

func signalFromDispatchError(err error, c gateway.Classification) channelhealth.SignalClass {
	if dispatchErrorIsInfrastructure(err) {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return channelhealth.SignalTimeout
	}
	return gateway.SignalFromClassification(0, c)
}

func dispatchErrorIsInfrastructure(err error) bool {
	if err == nil {
		return false
	}
	var upstreamErr *gateway.UpstreamHTTPError
	if errors.As(err, &upstreamErr) {
		return false
	}
	switch transport.TransportErrorClassOf(err) {
	case transport.TransportErrorClassSidecarUnavailable,
		transport.TransportErrorClassSidecarProfileUnavailable:
		return true
	}
	switch gateway.TransportErrorClassFromError(err) {
	case gateway.TransportErrorTLSHandshakeFailed,
		gateway.TransportErrorConnectionRefused,
		gateway.TransportErrorDNSFailure,
		gateway.TransportErrorNetworkUnreachable,
		gateway.TransportErrorProxyFailure:
		return true
	default:
		return false
	}
}

func rateLimitResetFromClassification(c gateway.Classification, now time.Time) *time.Time {
	if c.RetryAfterMs <= 0 {
		return nil
	}
	reset := now.Add(time.Duration(c.RetryAfterMs) * time.Millisecond)
	return &reset
}

func (ex *chatExecution) applyUpstreamErrorCooldown(upstreamErr *gateway.UpstreamHTTPError, classification gateway.Classification, applyAccountCooldown bool) bool {
	if ex == nil || upstreamErr == nil {
		return false
	}
	if upstreamErr.StatusCode == http.StatusNotFound {
		recordModelCooldownFromUpstreamError(ex.ctx, ex.d, ex.ident.TenantID, ex.acquiredAccountID, ex.upstreamModelID, upstreamErr.StatusCode, ex.requestID, nil, rate.ReasonModelLimitExceeded)
		return false
	}
	if upstreamErr.StatusCode == http.StatusTooManyRequests {
		if classification.Class != gateway.ErrorClassRateLimited {
			return false
		}
		dec, hasDecision := ex.upstreamRateDecision(upstreamErr)
		if hasDecision && dec.StateChange != rate.StateNoChange && dec.StateChange != rate.StateRateLimited {
			if applyAccountCooldown {
				ex.forceCooldownFromDecision(dec)
			}
			return false
		}
		resetAt := rateLimitResetFromClassification(classification, time.Now())
		reason := rate.ReasonRateLimitRPM
		if hasDecision {
			if !dec.CooldownUntil.IsZero() {
				reset := dec.CooldownUntil
				resetAt = &reset
			}
			if dec.Reason != "" {
				reason = dec.Reason
			}
		}
		// 只有模型格确实写成功才抑制整号健康信号;写失败/未接线(ModelCooldowns nil、
		// modelKey 空、DB 落库报错)时返回 false,让调用方回落 recordChannelHealthSignal——
		// 否则纯 429 会既没写模型格又跳过账号健康,该账号该模型零冷却、被立刻重选反复挨限速。
		return recordModelCooldownFromUpstreamError(ex.ctx, ex.d, ex.ident.TenantID, ex.acquiredAccountID, ex.upstreamModelID, upstreamErr.StatusCode, ex.requestID, resetAt, reason)
	}
	if applyAccountCooldown {
		ex.forceCooldownFromUpstreamRateLimit(upstreamErr)
	}
	return false
}

func (ex *chatExecution) upstreamRateDecision(upstreamErr *gateway.UpstreamHTTPError) (rate.Decision, bool) {
	if ex == nil || upstreamErr == nil || ex.d.RateService == nil {
		return rate.Decision{}, false
	}
	dec, err := ex.d.RateService.HandleUpstreamError(ex.ctx, ex.acquiredAccountID, upstreamErr.StatusCode, upstreamErr.Header, upstreamErr.Body)
	if err != nil {
		logInternalError(ex.ctx, ex.requestID, "upstream_rate_cooldown_decision_failed", err)
		return rate.Decision{}, false
	}
	return dec, true
}

// recordModelCooldownFromUpstreamError 写一格账号×模型冷却,返回是否确实写成功。
// 返回值供调用方决定要不要抑制整号健康信号:只有 true(确认落库)才代表模型格已承接冷却,
// false(未接线/参数不全/落库报错)时调用方须回落账号级健康信号,避免该模型零冷却被反复重选。
func recordModelCooldownFromUpstreamError(ctx context.Context, d ChatHandlerDeps, tenantID, accountID int64, modelKey string, statusCode int, requestID string, resetAt *time.Time, reason rate.Reason) bool {
	if d.ModelCooldowns == nil || tenantID == 0 || accountID == 0 || modelKey == "" {
		return false
	}
	switch statusCode {
	case http.StatusNotFound:
		if reason == "" {
			reason = rate.ReasonModelLimitExceeded
		}
	case http.StatusTooManyRequests:
		if reason == "" {
			reason = rate.ReasonRateLimitRPM
		}
	default:
		return false
	}
	input := rate.ModelCooldownInput{
		TenantID:          tenantID,
		ProviderAccountID: accountID,
		ModelKey:          modelKey,
		Reason:            reason,
		StatusCode:        statusCode,
		UpstreamRequestID: requestID,
	}
	if resetAt != nil {
		input.ResetAt = *resetAt
	}
	if err := d.ModelCooldowns.RecordModelRateLimit(ctx, input); err != nil {
		logInternalError(ctx, requestID, "model_rate_limit_record_failed", err)
		return false
	}
	return true
}
