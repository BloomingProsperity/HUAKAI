package completionshttp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	fallbackexec "github.com/BloomingProsperity/HUAKAI/internal/bindingfallback/executor"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

// settleRecoveryTimeout 给脱钩后的交付后结算 ctx 一个上限，防止 Tx2 永久挂住。
// 与 gatewayhttp chat 流式路径同值(30s)。
const settleRecoveryTimeout = 30 * time.Second

func (ex *execution) selectAccount(w http.ResponseWriter, attemptSeq int, requestedModel string) *fallbackexec.Failure {
	claimID := int64(0)
	if ex.reserveRes != nil {
		claimID = ex.reserveRes.ClaimID
	}
	sel, err := ex.d.Selector.Select(ex.ctx, pool.SelectionRequest{
		TenantID:             ex.ident.TenantID,
		UserID:               ex.ident.UserID,
		APIKeyID:             ex.ident.APIKeyID,
		PoolGroupID:          ex.attempt.PoolGroupID,
		RequestedModel:       requestedModel,
		ProviderModelID:      ex.upstreamModelID,
		ModelCooldownKey:     ex.upstreamModelID,
		ProtocolFamily:       ex.resolved.ProtocolFamily,
		EndpointFamily:       ex.endpointFamily,
		ClaimID:              claimID,
		AttemptSeq:           attemptSeq,
		CapabilityFlags:      ex.attempt.RequiredCapabilities,
		SessionHash:          ex.payloadHash,
		RequestID:            ex.requestID,
		Vendor:               pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
		UserGroup:            ex.ident.UserGroup,
		SelectionMode:        ex.attempt.SelectionMode,
		BindingID:            ex.attempt.BindingID,
		BindingRPMLimit:      ex.attempt.BindingRPMLimit,
		BindingTPMLimit:      ex.attempt.BindingTPMLimit,
		MaxParallelRequests:  ex.attempt.MaxParallelRequests,
		EstimatedInputTokens: ex.inputEstimate,
		ModelContextWindow:   ex.resolved.ContextWindow,
		MaxOutputTokens:      requestedMaxTokens(ex.req),
		ExcludedAccounts:     ex.excludedAccounts,
	})
	if err != nil || sel == nil || sel.AccountID == 0 || sel.WaitPlan != nil {
		failureErr := err
		if failureErr == nil {
			failureErr = pool.ErrNoEligibleAccount
			if sel != nil && sel.WaitPlan != nil {
				failureErr = pool.ErrNoSlotAvailable
			}
		}
		failure := fallbackexec.PoolFailure(failureErr)
		if !ex.abort(w, failure.AbortReason, 0) {
			return fallbackexec.AbortFailure()
		}
		return failure
	}
	if ex.classTransition != nil && bindingfallback.NormalizeClass(string(ex.attempt.FallbackClass)) != bindingfallback.ClassNormal {
		sel.RoutingReasonJSON = bindingfallback.AnnotateRoutingReason(sel.RoutingReasonJSON, *ex.classTransition)
	}
	ex.selRes = sel
	return nil
}

func (ex *execution) resolveCredential(w http.ResponseWriter) bool {
	cred, acc, err := ex.d.CredentialVault.Resolve(ex.ctx, ex.ident.TenantID, ex.selRes.AccountID)
	if err != nil {
		if !ex.abort(w, "credential_resolve_error", 0) {
			fallbackexec.WriteHTTP(w, fallbackexec.AbortFailure())
		} else {
			writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeCredentialResolveError, clienterr.MessageFor(clienterr.CodeCredentialResolveError))
		}
		return false
	}
	if acc.AccountID == 0 {
		acc.AccountID = ex.selRes.AccountID
	}
	ex.cred = cred
	ex.accInfo = acc
	return true
}

// credentialCompatibilityFailure 发网前校验凭据形态与协议族匹配(oauth 号不能打
// api-key 直连等)。不匹配=本号静态必败:退预留、经授权换号子预算换下一个号,
// 绝不带着错配凭据发网(上游 401 白烧一轮还可能触发风控)。
func (ex *execution) credentialCompatibilityFailure(w http.ResponseWriter) *fallbackexec.Failure {
	if err := servingcapability.ValidateRuntimeAccountCompatibility(ex.resolved.ProtocolFamily, ex.cred, ex.accInfo); err == nil {
		return nil
	}
	failure := fallbackexec.CredentialCompatibilityFailure()
	if !ex.abort(w, failure.AbortReason, 0) {
		return fallbackexec.AbortFailure()
	}
	return failure
}

func (ex *execution) dispatchCompletionsAndSettle(w http.ResponseWriter, attemptSeq int) attemptOutcome {
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    ex.upstreamPath,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.body,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	})
	if err != nil {
		ex.observeDispatchError(err)
		failure := fallbackexec.DispatchFailure(err)
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return attemptOutcome{failure: failure}
	}
	if res == nil || res.UpstreamReader == nil {
		ex.observeChannelError(0)
		failure := fallbackexec.EmptyResponseFailure()
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return attemptOutcome{failure: failure}
	}
	defer closeDispatchResult(res)
	return ex.finishCompletionsResponse(w, res, attemptSeq)
}

func (ex *execution) finishCompletionsResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int) attemptOutcome {
	if ex.req.Stream || strings.Contains(strings.ToLower(res.Headers.Get("Content-Type")), "text/event-stream") {
		return ex.finishStreamingResponse(w, res, attemptSeq)
	}
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	if readErr != nil {
		ex.observeChannelError(res.StatusCode)
		failure := fallbackexec.ReadFailure(readErr)
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return attemptOutcome{failure: failure}
	}
	// 非 2xx 必须先于空 body 判定:400/401 常带空 body,先判空会把终态客户端错误
	// 伪装成可重试的 empty_response。
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		observed := ex.observeHTTPError(res, raw)
		failure := fallbackexec.UpstreamFailureFromDecision(res.StatusCode, raw, observed.Decision, observed.Classification)
		if ex.abortWithError(w, failure.AbortReason, 0) != nil {
			// abort 失败=预留状态不明,终态不再换号(防双份扣费);仍按上游语义回
			// 客户端,X-Huakai-Abort-Failed 头已由 abort 助手落下。
			failure.RetryPermitted = false
			failure.AuthFailoverEligible = false
		}
		return attemptOutcome{failure: failure}
	}
	if strings.TrimSpace(string(raw)) == "" {
		failure := fallbackexec.EmptyResponseFailure()
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return attemptOutcome{failure: failure}
	}
	_ = ex.settleAndWriteJSON(w, res, raw, attemptSeq)
	return attemptOutcome{done: true}
}

func (ex *execution) settleAndWriteJSON(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int) bool {
	usage, ok := usageFromJSON(raw)
	if !ok {
		ex.observeChannelError(res.StatusCode)
		ex.abort(w, "usage_missing", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, clienterr.MessageFor(clienterr.CodeCanonicalResponseError))
		return false
	}
	ex.observeSuccess(res)
	cost, err := ex.actualCost(usage)
	if err != nil {
		ex.abort(w, "pricing_unavailable", int64(usage.PromptTokens))
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	settleReq := ex.settleRequest(usage, cost, attemptSeq, false)
	if !ex.openDeliveryGate(w, int64(usage.PromptTokens)) {
		return false
	}
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	written, writeErr := w.Write(raw)
	fullyWritten := written >= len(raw)
	if !fullyWritten && writeErr == nil {
		writeErr = io.ErrShortWrite
	}
	if !fullyWritten {
		_ = ex.abortWithError(w, "client_response_write_error", int64(usage.PromptTokens))
		return false
	}
	if writeErr != nil {
		ex.logResponseDeliveryUncertain(writeErr)
	}
	// 小 JSON 仍可能停在 net/http 缓冲；结算前主动刷新。完整 Write 后的刷新
	// 错误属于交付不确定，按已交付保守结算，不能释放预留。
	if flushErr := http.NewResponseController(w).Flush(); flushErr != nil {
		ex.logResponseDeliveryUncertain(flushErr)
	}
	// 响应完整交付后再结算；结算必须在脱钩 ctx 上跑，否则客户端断连会
	// 回滚 Tx2。失败经 DLQ 持久重试，不能把已交付的 200 改写成假 500。
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ex.ctx), settleRecoveryTimeout)
	defer cancel()
	_ = ex.settleDirectWithRecovery(settleCtx, settleReq)
	return true
}

func (ex *execution) finishStreamingResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int) attemptOutcome {
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, readErr := readUpstreamBody(res.UpstreamReader)
		if readErr != nil {
			ex.observeChannelError(res.StatusCode)
			failure := fallbackexec.ReadFailure(readErr)
			if !ex.abort(w, failure.AbortReason, 0) {
				failure = fallbackexec.AbortFailure()
			}
			return attemptOutcome{failure: failure}
		}
		observed := ex.observeHTTPError(res, raw)
		failure := fallbackexec.UpstreamFailureFromDecision(res.StatusCode, raw, observed.Decision, observed.Classification)
		if ex.abortWithError(w, failure.AbortReason, 0) != nil {
			failure.RetryPermitted = false
			failure.AuthFailoverEligible = false
		}
		return attemptOutcome{failure: failure}
	}
	buffered := bufio.NewReader(res.UpstreamReader)
	if _, err := buffered.Peek(1); err != nil {
		failure := fallbackexec.ReadFailure(err)
		if errors.Is(err, io.EOF) {
			failure = fallbackexec.EmptyResponseFailure()
		}
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return attemptOutcome{failure: failure}
	}
	if !ex.openDeliveryGate(w, int64(ex.inputEstimate)) {
		return attemptOutcome{done: true}
	}

	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.WriteHeader(http.StatusOK)

	var copied bytes.Buffer
	streamErr := streamAndCapture(w, buffered, &copied)
	if streamErr != nil && copied.Len() == 0 {
		// 真零交付:头已 200 但没有任何字节发给客户端、也没捕获到上游内容。此时无可计费交付,
		// 释放整笔预留是正确的(对齐 chat 流式"仅真零交付才 abort")。
		ex.observeChannelError(res.StatusCode)
		ex.abort(w, clienterr.CodeUpstreamReadError, 0)
		return attemptOutcome{done: true}
	}
	// 到这里:要么流干净结束,要么中途出错但已有部分交付(copied.Len()>0)。已交付内容对应的
	// token 上游已生成并会向平台计费,从此处起任何分支都**不得 abort 退款**——否则用户白拿、
	// 平台吃下上游成本。只能"尽力计费、不足则以 PendingReconciliation 标记待对账并留审计行"。
	if streamErr != nil {
		ex.observeChannelError(res.StatusCode)
	} else {
		ex.observeSuccess(res)
	}
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
		return attemptOutcome{done: true}
	}
	return attemptOutcome{done: true}
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
			appendBoundedStreamTail(captured, chunk, maxUpstreamBodyBytes)
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

// appendBoundedStreamTail 只保留流末尾的有界窗口。上游通常在终止帧报告 usage，
// 因此前缀超过窗口时不能停止采集，否则大响应会稳定丢失真实计费数据。
func appendBoundedStreamTail(dst *bytes.Buffer, chunk []byte, limit int) {
	if dst == nil || limit <= 0 || len(chunk) == 0 {
		return
	}
	if len(chunk) >= limit {
		dst.Reset()
		_, _ = dst.Write(chunk[len(chunk)-limit:])
		return
	}
	overflow := dst.Len() + len(chunk) - limit
	if overflow > 0 {
		dst.Next(overflow)
	}
	_, _ = dst.Write(chunk)
}

func closeDispatchResult(res *gateway.DispatchResult) {
	if res != nil && res.Close != nil {
		_ = res.Close()
	}
}

// feedbackAttempt 组装喂给账号健康 FSM 的一次尝试上下文。
func (ex *execution) feedbackAttempt() upstreamfeedback.Attempt {
	attempt := upstreamfeedback.Attempt{
		TenantID:       ex.ident.TenantID,
		Account:        ex.accInfo,
		ProtocolFamily: ex.resolved.ProtocolFamily,
		ModelKey:       ex.upstreamModelID,
		RequestID:      ex.requestID,
		StartedAt:      ex.startedAt,
	}
	if binding, ok := ex.resolved.BindingForAttempt(ex.attempt.BindingID, ex.attempt.PoolGroupID); ok {
		attempt.StatusCodeMapping = binding.StatusCodeMapping
	}
	return attempt
}

// observeDispatchError/observeChannelError/observeHTTPError/observeSuccess 把上游结果喂给账号健康
// FSM(upstreamfeedback→channelhealth.ApplySignal),坏号据此进入冷却→下次选号自动跳过(自动换号)。
// 决策/路由归 fallbackexec；Observer 追加账号客户端投影与健康副作用，nil 时仍用同一静态分类。
func (ex *execution) observeDispatchError(err error) {
	if ex.d.Feedback != nil {
		_ = ex.d.Feedback.ObserveDispatchError(ex.ctx, ex.feedbackAttempt(), err)
	}
}

func (ex *execution) observeChannelError(statusCode int) {
	if ex.d.Feedback != nil {
		ex.d.Feedback.ObserveChannelError(ex.ctx, ex.feedbackAttempt(), statusCode)
	}
}

func (ex *execution) observeHTTPError(res *gateway.DispatchResult, raw []byte) upstreamfeedback.HTTPFailure {
	attempt := ex.feedbackAttempt()
	if ex.d.Feedback != nil {
		return ex.d.Feedback.ObserveHTTPError(ex.ctx, attempt, res.StatusCode, res.Headers, raw)
	}
	return upstreamfeedback.ClassifyHTTPError(attempt, res.StatusCode, res.Headers, raw)
}

func (ex *execution) observeSuccess(res *gateway.DispatchResult) {
	if ex.d.Feedback != nil {
		ex.d.Feedback.ObserveSuccess(ex.ctx, ex.feedbackAttempt(), res.StatusCode, res.Headers)
	}
}

// excludeAccount 把本次失败账号加入本请求排除集,重试选号经 SelectionRequest.ExcludedAccounts
// 被 pool/router gates+pasr 跳过,避免重试打到同一坏号。
func (ex *execution) excludeAccount(accountID int64) {
	if accountID <= 0 {
		return
	}
	if ex.excludedAccounts == nil {
		ex.excludedAccounts = make(map[int64]struct{})
	}
	ex.excludedAccounts[accountID] = struct{}{}
}
