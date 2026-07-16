package completionshttp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

// settleRecoveryTimeout 给脱钩后的交付后结算 ctx 一个上限，防止 Tx2 永久挂住。
// 与 gatewayhttp chat 流式路径同值(30s)。
const settleRecoveryTimeout = 30 * time.Second

// settleRecoveryEnqueueTimeout 给 DLQ 兜底 enqueue 一个独立(不复用已过期 settle ctx)的上限。
// 交付后结算因 deadline 超时失败时，recovery intent 仍须落盘，故 enqueue 用 fresh ctx。
const settleRecoveryEnqueueTimeout = 10 * time.Second

func (ex *execution) selectAccount(w http.ResponseWriter, attemptSeq int, requestedModel string) bool {
	claimID := int64(0)
	if ex.reserveRes != nil {
		claimID = ex.reserveRes.ClaimID
	}
	sel, err := ex.d.Selector.Select(ex.ctx, pool.SelectionRequest{
		TenantID:         ex.ident.TenantID,
		UserID:           ex.ident.UserID,
		APIKeyID:         ex.ident.APIKeyID,
		PoolGroupID:      ex.attempt.PoolGroupID,
		RequestedModel:   requestedModel,
		ModelCooldownKey: ex.upstreamModelID,
		ProtocolFamily:   ex.resolved.ProtocolFamily,
		EndpointFamily:   ex.endpointFamily,
		ClaimID:          claimID,
		AttemptSeq:       attemptSeq,
		ExcludedAccounts: ex.excludedAccounts,
		CapabilityFlags:  ex.attempt.RequiredCapabilities,
		SessionHash:      ex.payloadHash,
		Vendor:           pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
		UserGroup:        ex.ident.UserGroup,
	})
	if err != nil || sel == nil || sel.AccountID == 0 || sel.WaitPlan != nil {
		ex.abort(w, "pool_select_no_account", 0)
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeNoCapacity, clienterr.MessageFor(clienterr.CodeNoCapacity))
		return false
	}
	ex.selRes = sel
	return true
}

func (ex *execution) resolveCredential(w http.ResponseWriter) bool {
	cred, acc, err := ex.d.CredentialVault.Resolve(ex.ctx, ex.ident.TenantID, ex.selRes.AccountID)
	if err != nil {
		ex.abort(w, "credential_resolve_error", 0)
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeCredentialResolveError, clienterr.MessageFor(clienterr.CodeCredentialResolveError))
		return false
	}
	if acc.AccountID == 0 {
		acc.AccountID = ex.selRes.AccountID
	}
	ex.cred = cred
	ex.accInfo = acc
	return true
}

type attemptFailure struct {
	Decision       gateway.AttemptRetryDecision
	Classification gateway.Classification
	AbortErr       error
}

type attemptOutcome struct {
	Done    bool
	Failure *attemptFailure
}

func (ex *execution) dispatchCompletionsAndSettle(w http.ResponseWriter, attemptSeq int) attemptOutcome {
	startedAt := time.Now()
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    ex.upstreamPath,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.body,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	})
	if err != nil {
		decision := dispatchFailureDecision(err)
		if ex.d.Feedback != nil {
			decision = normalizeDispatchFailureDecision(
				ex.d.Feedback.ObserveDispatchError(ex.ctx, ex.feedbackAttempt(startedAt), err),
			)
		}
		return attemptOutcome{Failure: &attemptFailure{
			Decision: decision,
			AbortErr: ex.abortWithError(w, decision.AbortReason, 0),
		}}
	}
	if res == nil || res.UpstreamReader == nil {
		ex.observeChannelError(startedAt, 0)
		decision := retryableAttemptDecision("upstream_empty_response", http.StatusBadGateway)
		return attemptOutcome{Failure: &attemptFailure{
			Decision: decision,
			AbortErr: ex.abortWithError(w, decision.AbortReason, 0),
		}}
	}
	defer closeDispatchResult(res)
	return ex.finishCompletionsResponse(w, res, attemptSeq, startedAt)
}

func (ex *execution) finishCompletionsResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int, startedAt time.Time) attemptOutcome {
	if ex.req.Stream || strings.Contains(strings.ToLower(res.Headers.Get("Content-Type")), "text/event-stream") {
		return ex.finishStreamingResponse(w, res, attemptSeq, startedAt)
	}
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	if readErr != nil {
		ex.observeChannelError(startedAt, res.StatusCode)
		ex.abort(w, clienterr.CodeUpstreamReadError, 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
		return attemptOutcome{Done: true}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		failure := ex.observeHTTPFailure(startedAt, res, raw)
		failure.AbortErr = ex.abortWithError(w, failure.Decision.AbortReason, 0)
		return attemptOutcome{Failure: failure}
	}
	_ = ex.settleAndWriteJSON(w, res, raw, attemptSeq, startedAt)
	return attemptOutcome{Done: true}
}

func (ex *execution) settleAndWriteJSON(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int, startedAt time.Time) bool {
	usage, ok := usageFromJSON(raw)
	if !ok {
		ex.observeChannelError(startedAt, res.StatusCode)
		ex.abort(w, "usage_missing", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, clienterr.MessageFor(clienterr.CodeCanonicalResponseError))
		return false
	}
	ex.observeSuccess(startedAt, res)
	cost, err := ex.actualCost(usage)
	if err != nil {
		ex.abort(w, "pricing_unavailable", int64(usage.PromptTokens))
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	// 交付后结算:上游 2xx body 已读回(平台已付费),结算必须在**脱钩 ctx**(WithoutCancel)上跑,
	// 否则客户端在 body 读完、Tx2 未 commit 的窗口断连会取消请求 ctx → Tx2 回滚 → 已交付 token 永不
	// 计费 + claim/hold/账号槽/配额预留冻结到 lease 过期。与流式路径、abort 路径同脱钩范式;失败经 DLQ 持久重试。
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ex.ctx), settleRecoveryTimeout)
	defer cancel()
	if err := ex.settleDirectWithRecovery(settleCtx, ex.settleRequest(usage, cost, attemptSeq, false)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeSettleError, clienterr.MessageFor(clienterr.CodeSettleError))
		return false
	}
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
	return true
}

func (ex *execution) finishStreamingResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int, startedAt time.Time) attemptOutcome {
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, readErr := readUpstreamBody(res.UpstreamReader)
		if readErr != nil {
			ex.observeChannelError(startedAt, res.StatusCode)
			ex.abort(w, clienterr.CodeUpstreamReadError, 0)
			writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
			return attemptOutcome{Done: true}
		}
		failure := ex.observeHTTPFailure(startedAt, res, raw)
		failure.AbortErr = ex.abortWithError(w, failure.Decision.AbortReason, 0)
		return attemptOutcome{Failure: failure}
	}

	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.WriteHeader(http.StatusOK)

	var copied bytes.Buffer
	streamErr := streamAndCapture(w, res.UpstreamReader, &copied)
	if streamErr != nil && copied.Len() == 0 {
		// 真零交付:头已 200 但没有任何字节发给客户端、也没捕获到上游内容。此时无可计费交付,
		// 释放整笔预留是正确的(对齐 chat 流式"仅真零交付才 abort")。
		ex.observeChannelError(startedAt, res.StatusCode)
		ex.abort(w, clienterr.CodeUpstreamReadError, 0)
		return attemptOutcome{Done: true}
	}
	if streamErr != nil {
		ex.observeChannelError(startedAt, res.StatusCode)
	} else {
		ex.observeSuccess(startedAt, res)
	}

	// 到这里:要么流干净结束,要么中途出错但已有部分交付(copied.Len()>0)。已交付内容对应的
	// token 上游已生成并会向平台计费,从此处起任何分支都**不得 abort 退款**——否则用户白拿、
	// 平台吃下上游成本。只能"尽力计费、不足则以 PendingReconciliation 标记待对账并留审计行"。
	usage, ok := usageFromSSE(copied.Bytes())
	if streamErr != nil {
		// 流中途中断:即便从部分 SSE 解析出 usage 也不可信(只覆盖了部分输出),按缺失处理走待对账。
		ok = false
	}
	if !ok {
		usage = completionUsage{PromptTokens: ex.inputEstimate}
	}

	cost, costErr := ex.actualCost(usage)
	if costErr != nil {
		// 定价此刻不可用(费率表缺失/解析失败等),但交付已发生:绝不能退款。以零成本占位并标
		// PendingReconciliation,留下一条可对账的 usage_record 审计行(供运维/后续对账消费者补价),
		// 而不是 abort 把已交付内容退成 0。注意:此处不假设有 worker 自动按真实价表重算——下方
		// settlementrecovery DLQ 仅在 settle 本身失败时重放、保证这条待对账行最终落库,并不重算金额。
		cost = completionCostBreakdown{}
		ok = false
	}
	if !ok {
		cost.PendingReconciliation = true
		cost.CostSnapshot = appendStreamPendingReason(cost.CostSnapshot, streamErr, costErr)
	}

	// 交付后结算：响应已 flush 给客户端。结算必须在**脱钩 ctx**(WithoutCancel)上跑，
	// 否则客户端在流末断连会取消请求 ctx → Tx2 立即失败 → 已交付 token 永不计费(S1-2)。
	// settle 失败时经 settlementrecovery DLQ 持久重试，防不可恢复钱丢失(S1-3)。
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ex.ctx), settleRecoveryTimeout)
	defer cancel()
	if err := ex.settleStreamWithRecovery(settleCtx, ex.settleRequest(usage, cost, attemptSeq, true)); err != nil {
		w.Header().Set("X-Huakai-Settle-Failed", clienterr.CodeSettleError)
		return attemptOutcome{Done: true}
	}
	return attemptOutcome{Done: true}
}

// appendStreamPendingReason 给交付后待对账的成本快照追加一个 pending 原因标记,供
// settlementrecovery worker 与审计区分"这条已交付的流为何被挂起待对账"。三种因由按优先级:
// 流中途中断 > 定价此刻不可用 > 仅缺上游 usage 帧。返回追加后的快照(原快照为空时直接用标记)。
func appendStreamPendingReason(snapshot string, streamErr, costErr error) string {
	reason := "stream_usage_missing"
	switch {
	case streamErr != nil:
		reason = "stream_interrupted"
	case costErr != nil:
		reason = "pricing_unavailable"
	}
	marker := "pending_reconciliation=" + reason
	if snapshot == "" {
		return marker
	}
	return snapshot + ";" + marker
}

func streamAndCapture(w http.ResponseWriter, r io.Reader, captured *bytes.Buffer) error {
	buf := make([]byte, 8192)
	controller := http.NewResponseController(w)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if captured.Len()+len(chunk) <= maxUpstreamBodyBytes {
				_, _ = captured.Write(chunk)
			}
			if _, err := w.Write(chunk); err != nil {
				return err
			}
			_ = controller.Flush()
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func upstreamAbortReason(status int, headers http.Header, raw []byte) string {
	decision, _, err := gateway.ClassifyAttemptHTTPError(status, headers, raw, "")
	if err != nil || decision.AbortReason == "" {
		return "upstream_error"
	}
	return decision.AbortReason
}

func (ex *execution) feedbackAttempt(startedAt time.Time) upstreamfeedback.Attempt {
	return upstreamfeedback.Attempt{
		TenantID:       ex.ident.TenantID,
		Account:        ex.accInfo,
		ProtocolFamily: ex.resolved.ProtocolFamily,
		ModelKey:       ex.upstreamModelID,
		RequestID:      ex.requestID,
		StartedAt:      startedAt,
	}
}

func (ex *execution) observeHTTPFailure(startedAt time.Time, res *gateway.DispatchResult, raw []byte) *attemptFailure {
	if ex.d.Feedback != nil {
		observed := ex.d.Feedback.ObserveHTTPError(
			ex.ctx,
			ex.feedbackAttempt(startedAt),
			res.StatusCode,
			res.Headers,
			raw,
		)
		return &attemptFailure{
			Decision:       observed.Decision,
			Classification: observed.Classification,
		}
	}
	decision, classification, err := gateway.ClassifyAttemptHTTPError(res.StatusCode, res.Headers, raw, ex.accInfo.Platform)
	if err != nil {
		decision = gateway.AttemptRetryDecision{
			ClientStatus: http.StatusBadGateway,
			AbortReason:  upstreamAbortReason(res.StatusCode, res.Headers, raw),
		}
	}
	return &attemptFailure{Decision: decision, Classification: classification}
}

func (ex *execution) observeChannelError(startedAt time.Time, statusCode int) {
	if ex.d.Feedback != nil {
		ex.d.Feedback.ObserveChannelError(ex.ctx, ex.feedbackAttempt(startedAt), statusCode)
	}
}

func (ex *execution) observeSuccess(startedAt time.Time, res *gateway.DispatchResult) {
	if ex.d.Feedback != nil {
		ex.d.Feedback.ObserveSuccess(ex.ctx, ex.feedbackAttempt(startedAt), res.StatusCode, res.Headers)
	}
}

func (ex *execution) excludeAccount(accountID int64) {
	if accountID <= 0 {
		return
	}
	if ex.excludedAccounts == nil {
		ex.excludedAccounts = make(map[int64]struct{})
	}
	ex.excludedAccounts[accountID] = struct{}{}
}

func (ex *execution) prepareNextAttempt() {
	ex.reserveRes = nil
	ex.selRes = nil
	ex.accInfo = provider.AccountInfo{}
	ex.cred = provider.Credential{}
}

func retryableAttemptDecision(reason string, status int) gateway.AttemptRetryDecision {
	return gateway.AttemptRetryDecision{
		RetryableBeforeDelivery: true,
		SwitchAccount:           true,
		SwitchPool:              true,
		ClientStatus:            status,
		AbortReason:             reason,
	}
}

func dispatchFailureDecision(err error) gateway.AttemptRetryDecision {
	return normalizeDispatchFailureDecision(gateway.ClassifyAttemptDispatchError(err))
}

func normalizeDispatchFailureDecision(decision gateway.AttemptRetryDecision) gateway.AttemptRetryDecision {
	if !decision.RetryableBeforeDelivery && decision.TransportClass == gateway.TransportErrorLocalDispatch {
		decision.ClientStatus = http.StatusBadGateway
		decision.AbortReason = "upstream_dispatch_error"
	}
	if decision.AbortReason == "" {
		decision.AbortReason = "upstream_dispatch_error"
	}
	return decision
}

func shouldRetryFailure(plan router.RoutePlan, failure *attemptFailure, finalAttempt, authFailoverUsed bool) (bool, bool) {
	if failure == nil || failure.AbortErr != nil {
		return false, false
	}
	if failure.Decision.CountsAgainstAuthFailoverBudget && !authFailoverUsed {
		return true, true
	}
	if finalAttempt || !failure.Decision.RetryableBeforeDelivery {
		return false, false
	}
	endClass := gateway.EndClassFromAttempt(failure.Classification, failure.Decision)
	for _, allowed := range plan.RetryableEndClasses {
		if strings.TrimSpace(allowed) == string(endClass) {
			return true, false
		}
	}
	return false, false
}

func effectiveAttemptBudget(plan router.RoutePlan) int {
	if len(plan.Attempts) == 0 {
		return 0
	}
	if os.Getenv("HUAKAI_ATTEMPT_RETRY_ENABLED") == "0" {
		return 1
	}
	budget := plan.AttemptBudget
	if budget <= 0 || budget > len(plan.Attempts) {
		budget = len(plan.Attempts)
	}
	if budget < 1 {
		return 1
	}
	return budget
}

func writeAttemptFailure(w http.ResponseWriter, failure *attemptFailure) {
	status := http.StatusBadGateway
	code := clienterr.CodeUpstreamDispatchError
	if failure != nil {
		if failure.Decision.ClientStatus > 0 {
			status = failure.Decision.ClientStatus
		}
		if failure.Classification.Class != "" {
			code = "upstream_" + string(failure.Classification.Class)
		}
		if failure.Classification.RetryAfterMs > 0 {
			w.Header().Set("Retry-After", strconv.FormatInt((failure.Classification.RetryAfterMs+999)/1000, 10))
		}
	}
	writeJSONError(w, status, code, "upstream request failed")
}

func closeDispatchResult(res *gateway.DispatchResult) {
	if res != nil && res.Close != nil {
		_ = res.Close()
	}
}
