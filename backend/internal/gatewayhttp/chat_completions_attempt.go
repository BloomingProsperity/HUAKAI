package gatewayhttp

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

const pr3EffectiveAttemptBudget = 1

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
	Decision gateway.AttemptRetryDecision

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
		AbortReason:    decision.AbortReason,
		Cause:          cause,
	}
}

func effectiveAttemptBudgetForPR3(plan router.RoutePlan) int {
	if len(plan.Attempts) == 0 {
		return 0
	}
	return pr3EffectiveAttemptBudget
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

// markAttemptOutcomeDelivered 把 outcome 标记为「已终结、handler 不再进下一 attempt」。
// PR3 权宜:交付前失败(选号 / 凭据 / dispatch 失败)也经此函数 —— 因为 PR3
// budget=1、retry 关闭,「写完错误响应即终止」与重构前行为逐字节一致。
// PR5 打开 retry 时:交付前的可重试失败必须改为返回带 Decision 的
// classifiedAttemptFailure,不能再无差别 markDelivered,否则 retry 进不来。
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
	if !ex.prepareClaimAndAccount(w, in) {
		return markAttemptOutcomeDelivered(out)
	}
	if !ex.resolveCredential(w) {
		out = ex.baseAttemptOutcome()
		return markAttemptOutcomeDelivered(out)
	}

	if !ex.req.Stream {
		// Anthropic buffered translator still fails closed before dispatch. 保持
		// 原有 abort + 501 行为，只是移动到单 attempt 执行体内。
		if ex.resolved.ProtocolFamily == "anthropic_messages" {
			if ex.reserveRes != nil {
				_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID,
					"buffered_anthropic_not_supported", ex.requestID, 0)
			}
			writeJSONError(w, http.StatusNotImplemented, "buffered_anthropic_not_supported",
				"Anthropic /v1/messages 非流式 (stream:false) 暂未实现; 请设 stream:true 走流式路径")
			out = ex.baseAttemptOutcome()
			return markAttemptOutcomeDelivered(out)
		}
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
		code = "attempt_failed"
	}
	message := failure.ClientMessage
	if message == "" {
		message = "upstream request failed"
	}
	if failure.Classification.RetryAfterMs > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", (failure.Classification.RetryAfterMs+999)/1000))
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
