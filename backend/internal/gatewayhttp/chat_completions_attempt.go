package gatewayhttp

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
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
	case gateway.TransportErrorTLSHandshakeFailed:
		return gateway.UpstreamError5xx
	}
	switch decision.AbortReason {
	case "upstream_5xx", "upstream_overloaded", "pool_no_capacity",
		"pool_select_error", "pool_select_no_account", "credential_resolve_error",
		"upstream_dispatch_error", "upstream_empty_response":
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
	if failure == nil || failure.DeliveredToClient || finalAttempt || !replayableBody {
		return false, false
	}
	normalRetry := failure.Decision.RetryableBeforeDelivery && retryableEndClassAllowed(plan.RetryableEndClasses, failure.EndClass)
	authRetry := failure.Decision.CountsAgainstAuthFailoverBudget && !authFailoverUsed
	if authRetry {
		return true, true
	}
	if normalRetry {
		return true, false
	}
	return false, false
}

var retryableAttemptScopedResponseHeaders = [...]string{
	"Trailer",
	"Cache-Control",
	"Connection",
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
