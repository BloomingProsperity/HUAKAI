package embeddingshttp

import (
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	fallbackexec "github.com/BloomingProsperity/HUAKAI/internal/bindingfallback/executor"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func (ex *execution) selectAccount(w http.ResponseWriter, attemptSeq int) *fallbackexec.Failure {
	sel, err := ex.d.Selector.Select(ex.ctx, pool.SelectionRequest{
		TenantID:            ex.ident.TenantID,
		UserID:              ex.ident.UserID,
		APIKeyID:            ex.ident.APIKeyID,
		PoolGroupID:         ex.attempt.PoolGroupID,
		RequestedModel:      ex.req.Model,
		ModelCooldownKey:    ex.upstreamModelID,
		ProtocolFamily:      ex.resolved.ProtocolFamily,
		EndpointFamily:      endpointFamilyEmbeddings,
		ClaimID:             ex.reserveRes.ClaimID,
		AttemptSeq:          attemptSeq,
		CapabilityFlags:     ex.attempt.RequiredCapabilities,
		SessionHash:         ex.payloadHash,
		Vendor:              pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
		UserGroup:           ex.ident.UserGroup,
		SelectionMode:       ex.attempt.SelectionMode,
		BindingID:           ex.attempt.BindingID,
		MaxParallelRequests: ex.attempt.MaxParallelRequests,
		ExcludedAccounts:    ex.excludedAccounts,
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

// embedAttemptDone = 成功交付或已写终态错误;embedAttemptRetryable = 投递前网络层失败,
// claim 已 abort、未写响应,可换账号重试。
func (ex *execution) dispatchAndSettle(w http.ResponseWriter, attemptSeq int) attemptOutcome {
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    upstreamEmbeddingsPath,
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
	defer func() {
		if res.Close != nil {
			_ = res.Close()
		}
	}()
	return ex.finishUpstreamResponse(w, res, attemptSeq)
}

func (ex *execution) finishUpstreamResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int) attemptOutcome {
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	if readErr != nil {
		ex.observeChannelError(res.StatusCode)
		failure := fallbackexec.ReadFailure(readErr)
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
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
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		ex.observeHTTPError(res, raw)
		failure := fallbackexec.UpstreamFailure(res.StatusCode, res.Headers, raw, ex.accInfo.Platform)
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return attemptOutcome{failure: failure}
	}
	_ = ex.settleSuccessfulResponse(w, res, raw, attemptSeq)
	return attemptOutcome{done: true}
}

func (ex *execution) settleSuccessfulResponse(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int) bool {
	promptTokens, ok := promptTokens(raw)
	if !ok {
		ex.observeChannelError(res.StatusCode)
		ex.abort(w, "usage_missing", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, clienterr.MessageFor(clienterr.CodeCanonicalResponseError))
		return false
	}
	ex.observeSuccess(res)
	actualCost, costSnapshot, pending, err := ex.inputCost(promptTokens)
	if err != nil {
		ex.abort(w, "pricing_unavailable", int64(promptTokens))
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	sbctx, scancel := ex.billingCtx()
	defer scancel()
	if _, err := ex.d.Settler.Settle(sbctx, ex.settleRequest(promptTokens, actualCost, costSnapshot, attemptSeq, pending)); err != nil {
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

// feedbackAttempt 组装喂给账号健康 FSM 的一次尝试上下文。
func (ex *execution) feedbackAttempt() upstreamfeedback.Attempt {
	return upstreamfeedback.Attempt{
		TenantID:       ex.ident.TenantID,
		Account:        ex.accInfo,
		ProtocolFamily: ex.resolved.ProtocolFamily,
		ModelKey:       ex.upstreamModelID,
		RequestID:      ex.requestID,
		StartedAt:      ex.startedAt,
	}
}

// observeDispatchError/observeChannelError/observeHTTPError/observeSuccess 把上游结果喂账号健康
// FSM(upstreamfeedback→channelhealth.ApplySignal),坏号据此冷却→下次选号自动跳过(自动换号)。
// 决策/路由归 fallbackexec,此处只取健康副作用,有返回值一律丢弃;nil Feedback 时 no-op。
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

func (ex *execution) observeHTTPError(res *gateway.DispatchResult, raw []byte) {
	if ex.d.Feedback != nil {
		_ = ex.d.Feedback.ObserveHTTPError(ex.ctx, ex.feedbackAttempt(), res.StatusCode, res.Headers, raw)
	}
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
