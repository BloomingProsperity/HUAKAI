package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bodyparamgate"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

type attemptInput struct {
	Plan             router.AttemptPlan
	AttemptSeq       int
	ExcludedAccounts map[int64]struct{}
	ReplayableBody   bool
	FinalAttempt     bool
}

type attemptOutcome struct {
	AttemptSeq       int
	Attempt          router.AttemptPlan
	AccountID        int64
	AcquisitionToken uuid.UUID
	Selection        *pool.SelectionResult

	Success         *attemptSuccess
	Failure         *classifiedAttemptFailure
	DeliveryStarted bool
	UsageDraft      gateway.UsageRecordDraft
	StreamAttempt   *billing.Attempt
}

type attemptSuccess struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Streamed   bool
}

type classifiedAttemptFailure struct {
	ClientStatus  int
	ClientCode    string
	ClientMessage string

	Classification gateway.Classification
	TransportClass gateway.TransportErrorClass
	// Decision 是 executor retry/failover 的单一事实来源。见综合稿 §3
	// override-1: 401 的 upstream_auth_failure 不在
	// RoutePlan.RetryableEndClasses 中，executor 必须同时检查
	// Decision.CountsAgainstAuthFailoverBudget。
	Decision          gateway.AttemptRetryDecision
	EndClass          gateway.StreamEndClass
	RetryAfterSeconds int

	DeliveredToClient bool
	AbortReason       string
	Cause             error
}

func classifiedFailureFromDecision(code, message string, classification gateway.Classification, decision gateway.AttemptRetryDecision, cause error) *classifiedAttemptFailure {
	return &classifiedAttemptFailure{
		ClientStatus:   decision.ClientStatus,
		ClientCode:     code,
		ClientMessage:  message,
		Classification: classification,
		TransportClass: decision.TransportClass,
		Decision:       decision,
		EndClass:       endClassFromAttemptFailure(classification, decision),
		AbortReason:    decision.AbortReason,
		Cause:          cause,
	}
}

func retryableLocalAttemptFailure(status int, code, message, abortReason string, endClass gateway.StreamEndClass, cause error) *classifiedAttemptFailure {
	failure := classifiedFailureFromDecision(code, message, gateway.Classification{}, gateway.AttemptRetryDecision{
		RetryableBeforeDelivery: true,
		SwitchAccount:           true,
		SwitchPool:              true,
		ClientStatus:            status,
		AbortReason:             abortReason,
	}, cause)
	failure.EndClass = endClass
	return failure
}

func terminalLocalAttemptFailure(status int, code, message, abortReason string, cause error) *classifiedAttemptFailure {
	return classifiedFailureFromDecision(code, message, gateway.Classification{}, gateway.AttemptRetryDecision{
		ClientStatus: status,
		AbortReason:  abortReason,
	}, cause)
}

func degradeFailureIfAbortFailed(ctx context.Context, requestID string, failure *classifiedAttemptFailure, abortErr error) *classifiedAttemptFailure {
	if failure == nil || abortErr == nil {
		return failure
	}
	logInternalError(ctx, requestID, clienterr.CodeAbortFailed, abortErr)
	failure.Decision.RetryableBeforeDelivery = false
	failure.Decision.CountsAgainstAuthFailoverBudget = false
	failure.Decision.SwitchAccount = false
	failure.Decision.SwitchPool = false
	reason := failure.AbortReason
	if reason == "" {
		reason = failure.Decision.AbortReason
	}
	if reason == "" {
		reason = "attempt_abort"
	}
	// Abort 失败时 claim 可能仍停在 reserving，禁止同一幂等键继续 retry。
	failure.AbortReason = reason + ";abort_failed=1"
	failure.Decision.AbortReason = failure.AbortReason
	return failure
}

func endClassFromAttemptFailure(classification gateway.Classification, decision gateway.AttemptRetryDecision) gateway.StreamEndClass {
	var draft gateway.UsageRecordDraft
	gateway.ApplyClassificationToDraft(&draft, classification)
	if draft.EndClass != "" && draft.EndClass != gateway.UnknownTermination {
		return draft.EndClass
	}
	switch decision.TransportClass {
	case gateway.TransportErrorConnectTimeout,
		gateway.TransportErrorNetworkTimeout,
		gateway.TransportErrorUpstreamHeaderTimeout,
		gateway.TransportErrorUpstreamBodyIdleTimeout:
		return gateway.InterEventTimeout
	case gateway.TransportErrorTLSHandshakeFailed,
		gateway.TransportErrorConnectionRefused,
		gateway.TransportErrorDNSFailure,
		gateway.TransportErrorNetworkUnreachable,
		gateway.TransportErrorProxyFailure:
		return gateway.UpstreamError5xx
	}
	switch decision.AbortReason {
	case "upstream_5xx", "upstream_overloaded", "pool_no_capacity",
		"pool_select_error", "pool_select_no_account", "credential_resolve_error",
		"upstream_dispatch_error", "upstream_empty_response",
		"transport_connection_refused", "transport_dns_failure",
		"transport_network_unreachable", "transport_proxy_failure":
		return gateway.UpstreamError5xx
	case "upstream_rate_limited", "queue_wait":
		return gateway.UpstreamRateLimit
	case "upstream_timeout", "transport_connect_timeout", "transport_network_timeout",
		"transport_upstream_header_timeout", "transport_upstream_body_idle_timeout":
		return gateway.InterEventTimeout
	}
	return gateway.UnknownTermination
}

func effectiveAttemptBudget(plan router.RoutePlan) int {
	if len(plan.Attempts) == 0 {
		return 0
	}
	if os.Getenv("HUAKAI_ATTEMPT_RETRY_ENABLED") == "0" {
		return 1
	}
	budget := plan.AttemptBudget
	if budget <= 0 {
		budget = len(plan.Attempts)
	}
	if budget > len(plan.Attempts) {
		budget = len(plan.Attempts)
	}
	if budget < 1 {
		return 1
	}
	return budget
}

func (ex *chatExecution) activeAttemptSeq() int {
	if ex.currentAttemptSeq > 0 {
		return ex.currentAttemptSeq
	}
	return 1
}

func (ex *chatExecution) baseAttemptOutcome() attemptOutcome {
	return attemptOutcome{
		AttemptSeq:       ex.activeAttemptSeq(),
		Attempt:          ex.attempt,
		AccountID:        ex.acquiredAccountID,
		AcquisitionToken: ex.acquisitionToken,
		Selection:        ex.selRes,
	}
}

func shouldRetryAttemptFailure(failure *classifiedAttemptFailure, plan router.RoutePlan, replayableBody bool, finalAttempt bool, authFailoverUsed bool) (bool, bool) {
	if failure == nil || failure.DeliveredToClient || !replayableBody {
		return false, false
	}
	// auth-failover 子预算独立于普通 attempt 预算: 401(可交付前)即使发生在最后一次普通 attempt,
	// 也应获得一次换号重试(设计见 gateway/attempt_error.go: "401 可交付前换一次号")。
	// !authFailoverUsed 限定至多一次; finalAttempt 门只挡普通重试。
	authRetry := failure.Decision.CountsAgainstAuthFailoverBudget && !authFailoverUsed
	if authRetry {
		return true, true
	}
	if finalAttempt {
		return false, false
	}
	normalRetry := failure.Decision.RetryableBeforeDelivery && retryableEndClassAllowed(plan.RetryableEndClasses, failure.EndClass)
	if normalRetry {
		return true, false
	}
	return false, false
}

var retryableAttemptScopedResponseHeaders = [...]string{
	"Trailer",
	"Cache-Control",
	"Connection",
	"X-Accel-Buffering",
	"X-HUAKAI-Cache-L2",
	headerHUAKAIAuditLedgerID,
	headerHUAKAIAuditLedgerDLQRef,
	headerHUAKAIAuditVerify,
	headerHUAKAIAuditSigFingerprint,
	headerHUAKAIStreamState,
	headerHUAKAIDeliveredTokens,
	headerHuakaiForwardError,
	headerHuakaiSettleError,
	headerHuakaiAbortFailed,
}

func clearRetryableAttemptFailureHeaders(w http.ResponseWriter) {
	if w == nil {
		return
	}
	for _, header := range retryableAttemptScopedResponseHeaders {
		w.Header().Del(header)
	}
}

// terminalFailureResidualHeaders 是终局 JSON 错误前要清的流式残留:SSE 格式
// 头 + 流计费状态头。与 retryableAttemptScopedResponseHeaders(重试间全清)
// 不同,诊断/审计标记在终局保留。
var terminalFailureResidualHeaders = [...]string{
	"Trailer",
	"Cache-Control",
	"Connection",
	"X-Accel-Buffering",
	"X-HUAKAI-Cache-L2",
	headerHUAKAIStreamState,
	headerHUAKAIDeliveredTokens,
}

func clearTerminalFailureResidualHeaders(w http.ResponseWriter) {
	if w == nil {
		return
	}
	for _, header := range terminalFailureResidualHeaders {
		w.Header().Del(header)
	}
}

func retryableEndClassAllowed(allowed []string, endClass gateway.StreamEndClass) bool {
	if endClass == "" {
		return false
	}
	for _, allowedClass := range allowed {
		if allowedClass == string(endClass) {
			return true
		}
	}
	return false
}

func (ex *chatExecution) prepareNextAttemptAfterAbort() {
	if ex == nil {
		return
	}
	ex.reserveRes = nil
	ex.selRes = nil
	ex.acquiredAccountID = 0
	ex.acquisitionToken = uuid.Nil
	ex.cred = provider.Credential{}
	ex.accInfo = provider.AccountInfo{}
	ex.forwardReq = gateway.ForwardRequest{}
	ex.healthKey = channelhealth.ChannelKey{}
	ex.healthKeyOK = false
}

func (ex *chatExecution) upstreamInboundBody(body []byte) []byte {
	if ex == nil || len(body) == 0 {
		return body
	}
	out := body
	// dify_chat 的出站 body 没有 model 字段(Dify 由 app token 决定模型,
	// 模型选择只参与路由/计费),不得把顶层 model 注进翻译产物污染契约。
	if strings.TrimSpace(ex.upstreamModelID) != "" && ex.resolved.ProtocolFamily != "dify_chat" {
		rewritten, ok := ex.rewriteUpstreamModel(body)
		if !ok {
			return body
		}
		out = rewritten
	}
	out = ex.stripCrossAccountResponseChain(out)
	override, stripKeys := ex.activeBodyParamGate()
	if len(override) == 0 && len(stripKeys) == 0 {
		return out
	}
	if len(override) > 0 {
		next, err := bodyparamgate.ApplyParamOverride(out, override)
		if err != nil {
			return out
		}
		out = next
	}
	if len(stripKeys) > 0 {
		next, err := bodyparamgate.StripBodyParams(out, stripKeys)
		if err != nil {
			return out
		}
		out = next
	}
	return out
}

func (ex *chatExecution) rewriteUpstreamModel(body []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body, false
	}
	modelRaw, err := json.Marshal(ex.upstreamModelID)
	if err != nil {
		return body, false
	}
	obj["model"] = modelRaw
	out, err := json.Marshal(obj)
	if err != nil {
		return body, false
	}
	return out, true
}

func (ex *chatExecution) activeBodyParamGate() (map[string]json.RawMessage, []string) {
	binding, ok := ex.activeBindingMetadata()
	if !ok {
		return nil, nil
	}
	return binding.ParamOverride, binding.BodyParamStrips
}

// markAttemptOutcomeDelivered 只用于已经写入客户端响应或明确不可进入
// retry/failover 的本地终止路径。交付前的可重试失败必须返回
// classifiedAttemptFailure，由 handler loop 根据双通道 retry gate 决策。
func markAttemptOutcomeDelivered(out attemptOutcome) attemptOutcome {
	out.DeliveryStarted = true
	if out.Failure == nil {
		out.Failure = &classifiedAttemptFailure{}
	}
	out.Failure.DeliveredToClient = true
	return out
}

func (ex *chatExecution) runAttempt(w http.ResponseWriter, in attemptInput) attemptOutcome {
	ex.activateRouteAttempt(in.Plan)
	ex.currentAttemptSeq = in.AttemptSeq

	out := ex.baseAttemptOutcome()
	if ok, failure := ex.prepareClaimAndAccount(w, in); !ok {
		out = ex.baseAttemptOutcome()
		if failure != nil {
			out.Failure = failure
			return out
		}
		return markAttemptOutcomeDelivered(out)
	}
	if failure := ex.resolveCredential(); failure != nil {
		out = ex.baseAttemptOutcome()
		out.Failure = failure
		return out
	}

	if !ex.req.Stream {
		return ex.executeNonStreamingAttempt(w)
	}
	return ex.executeStreamingAttempt(w)
}

func writeAttemptSuccess(w http.ResponseWriter, out attemptOutcome) {
	if out.Success == nil || out.Success.Streamed {
		return
	}
	if out.Success.Header != nil {
		for key, values := range out.Success.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
	}
	status := out.Success.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(out.Success.Body)
}

func writeAttemptFailure(w http.ResponseWriter, failure *classifiedAttemptFailure) {
	if failure == nil || failure.DeliveredToClient {
		return
	}
	// DM-19:终局 JSON 错误不得携带失败流式尝试的残留头(Trailer 声明/
	// X-Accel-Buffering/流计费状态头)——Trailer+JSON 会让严格客户端/中间层
	// 困惑,流计费头残留把"半截尝试"的状态泄进终局错误。诊断标记
	// (abort_failed/settle_error/forward_error)与审计头不在此列:终局错误
	// 正要靠它们暴露故障取证。Retry-After 在清理后设置。
	clearTerminalFailureResidualHeaders(w)
	status := failure.ClientStatus
	if status == 0 {
		status = http.StatusBadGateway
	}
	code := failure.ClientCode
	if code == "" && failure.Classification.Class != "" {
		code = "upstream_" + string(failure.Classification.Class)
	}
	if code == "" {
		code = clienterr.CodeAttemptFailed
	}
	message := failure.ClientMessage
	if message == "" {
		message = clienterr.MessageFor(code)
	}
	if failure.Classification.RetryAfterMs > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", (failure.Classification.RetryAfterMs+999)/1000))
	} else if failure.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", failure.RetryAfterSeconds))
	}
	writeJSONError(w, status, code, message)
}

type deliveryTracker struct {
	http.ResponseWriter
	startedFlag bool
	status      int
}

func newDeliveryTracker(w http.ResponseWriter) *deliveryTracker {
	return &deliveryTracker{ResponseWriter: w}
}

func (w *deliveryTracker) WriteHeader(statusCode int) {
	if !w.startedFlag {
		w.startedFlag = true
		w.status = statusCode
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *deliveryTracker) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 && !w.startedFlag {
		w.startedFlag = true
		w.status = http.StatusOK
	}
	return n, err
}

func (w *deliveryTracker) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *deliveryTracker) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *deliveryTracker) started() bool {
	return w != nil && w.startedFlag
}

func (w *deliveryTracker) statusCode() int {
	if w == nil || w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// stripCrossAccountResponseChain(DM-07):responses 协议的 previous_response_id
// 指向具体上游账号的 response 存储。sticky 未命中换号(绑定账号被健康门/
// 限流/重试排除挡掉)时,链 ID 跨账号原样转发上游必 404/400;剥掉它让请求
// 降级为无链续写成功,而非确定性失败。无 binding(短 prompt/TTL 过期)时
// 无法证明跨账号,保守不动(fail-open),避免误删同账号续写链。
func (ex *chatExecution) stripCrossAccountResponseChain(body []byte) []byte {
	if ex == nil || ex.clientProtocol != proto.ClientProtocolOpenAIResponses {
		return body
	}
	if ex.selRes == nil || ex.selRes.StickyState != pool.StickyStateMiss {
		return body
	}
	stripped, err := bodyparamgate.StripBodyParams(body, []string{"previous_response_id"})
	if err != nil {
		return body
	}
	if !bytes.Equal(stripped, body) {
		// 不记 body 内容(CMB-5),只留可观测痕迹。
		slog.InfoContext(ex.ctx, "responses previous_response_id stripped on sticky miss",
			slog.String("request_id", ex.requestID),
			slog.Int64("account_id", ex.accInfo.AccountID))
	}
	return stripped
}

// noCapacityFallbackRetryAfter 是无法估算池恢复时刻时的默认 Retry-After 秒数(沿用历史常数)。
const noCapacityFallbackRetryAfter = 5

// poolNoCapacityRetryAfter 从无容量错误里取池最早恢复时刻算 Retry-After 秒:错误携带未来恢复时刻 →
// ceil(恢复时刻 - now)(至少 1 秒);否则(非 NoCapacityError / 无可估时刻 / 时刻已过)回退默认值。
// 这样客户端按真实窗口边界退避(如上游长时限流),而非每隔固定 5 秒空撞 503。
func poolNoCapacityRetryAfter(err error, now time.Time) int {
	var noCap *pool.NoCapacityError
	if errors.As(err, &noCap) && noCap != nil && noCap.EarliestRecoveryAt.After(now) {
		// 整数向上取整,避免引入 math 依赖:(d + 1s - 1ns) / 1s。
		secs := int((noCap.EarliestRecoveryAt.Sub(now) + time.Second - time.Nanosecond) / time.Second)
		if secs < 1 {
			secs = 1
		}
		return secs
	}
	return noCapacityFallbackRetryAfter
}
