package rerankhttp

import (
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	fallbackexec "github.com/BloomingProsperity/HUAKAI/internal/bindingfallback/executor"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func (ex *execution) selectAccount(w http.ResponseWriter, attemptSeq int) *fallbackexec.Failure {
	sel, err := ex.d.Selector.Select(ex.ctx, pool.SelectionRequest{
		TenantID:             ex.ident.TenantID,
		UserID:               ex.ident.UserID,
		APIKeyID:             ex.ident.APIKeyID,
		PoolGroupID:          ex.attempt.PoolGroupID,
		RequestedModel:       ex.req.Model,
		ProviderModelID:      ex.upstreamModelID,
		ModelCooldownKey:     ex.upstreamModelID,
		ProtocolFamily:       ex.resolved.ProtocolFamily,
		EndpointFamily:       endpointFamilyRerank,
		ClaimID:              ex.reserveRes.ClaimID,
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

func (ex *execution) dispatchAndSettle(w http.ResponseWriter, attemptSeq int) attemptOutcome {
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    upstreamRerankPath,
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
	_ = ex.settleSuccessfulResponse(w, res, raw, attemptSeq)
	return attemptOutcome{done: true}
}

func (ex *execution) settleSuccessfulResponse(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int) bool {
	// 上游 2xx 已交付即记健康成功(本函数只在 2xx 调用),独立于下方结算成败——
	// 结算是计费关注点,不应决定账号健康信号(与 codex 语义一致)。
	ex.observeSuccess(res)
	settleReq := ex.settleRequest(ex.costSnapshot, attemptSeq)
	if !ex.openDeliveryGate(w, int64(ex.inputEstimate)) {
		return false
	}
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	written, writeErr := w.Write(raw)
	if writeErr != nil || written < len(raw) {
		_ = ex.abortWithError(w, "client_response_write_error", int64(ex.inputEstimate))
		return false
	}
	_ = http.NewResponseController(w).Flush()
	sbctx, scancel := ex.billingCtx()
	defer scancel()
	_ = ex.settleDeliveredResponse(sbctx, settleReq)
	return true
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

// observeDispatchError/observeChannelError/observeHTTPError/observeSuccess 把上游结果喂账号健康
// FSM(upstreamfeedback→channelhealth.ApplySignal),坏号据此冷却→下次选号自动跳过(自动换号)。
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
