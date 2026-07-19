package audiohttp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/audiopricing"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	fallbackexec "github.com/BloomingProsperity/HUAKAI/internal/bindingfallback/executor"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/relaybody"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
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
		EndpointFamily:      endpointFamilyAudio,
		ClaimID:             ex.reserveRes.ClaimID,
		AttemptSeq:          attemptSeq,
		CapabilityFlags:     ex.attempt.RequiredCapabilities,
		SessionHash:         ex.payloadHash,
		RequestID:           ex.requestID,
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
	// 把出站 body 的 model 改写成解析后的上游 id(JSON/multipart 皆可);multipart 会带新 boundary。
	inboundBody, inboundCT, _ := relaybody.RewriteModel(ex.body, ex.contentType, ex.upstreamModelID)
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:     ex.resolved.ProtocolFamily,
		EndpointPath:       ex.endpoint.Path(),
		UpstreamModelID:    ex.upstreamModelID,
		InboundBody:        inboundBody,
		InboundContentType: inboundCT,
		Account:            ex.accInfo,
		Credential:         ex.cred,
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
	if res.StatusCode >= 200 && res.StatusCode < 300 && ex.endpoint == audioEndpointSpeech {
		return ex.settleAndStreamSpeech(w, res, attemptSeq)
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
	_ = ex.settleSuccessfulResponse(w, res, raw, attemptSeq)
	return attemptOutcome{done: true}
}

func (ex *execution) settleAndStreamSpeech(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int) attemptOutcome {
	buffered := bufio.NewReader(res.UpstreamReader)
	if _, err := buffered.Peek(1); err != nil {
		ex.observeChannelError(res.StatusCode)
		failure := fallbackexec.ReadFailure(err)
		if err == io.EOF {
			failure = fallbackexec.EmptyResponseFailure()
		}
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return attemptOutcome{failure: failure}
	}
	// 重定价必须在写响应头之前:预扣用协议族 vendor 估的 predictedCost,选号后
	// 真账号平台(providerForPricing 此刻已含 accInfo.Platform)可能价不同,沿用
	// 预估会误扣/少收(与 per-second/token 在 settle 重算对称)。算价失败 → abort
	// 不计费,此刻响应未写可回 JSON 错误。
	actualCost, costSnapshot, pending, err := ex.charCost()
	if err != nil {
		ex.abort(w, "pricing_unavailable", 0)
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return attemptOutcome{done: true}
	}
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.WriteHeader(http.StatusOK)
	if _, cerr := io.Copy(w, buffered); cerr != nil {
		ex.abort(w, "client_delivery_failed", 0)
		return attemptOutcome{done: true}
	}
	ex.observeSuccess(res)
	// 音频完整交付后才结算,避免二进制断流误扣费(F1);结算走 billingCtx 防客户端断连取消(F2)。
	bctx, cancel := ex.billingCtx()
	defer cancel()
	// 交付后结算走 DLQ 兜底:响应已发出、settle 失败不能回 500,必须持久化 settle intent
	// 交统一 DLQ worker 幂等重放,防掉钱(codex 钱安全路径;#252 原实现只 log 会漏钱)。
	_ = ex.settleDeliveredResponse(bctx, ex.settleRequest(audioTokenUsage{}, actualCost, costSnapshot, attemptSeq, pending))
	return attemptOutcome{done: true}
}

func (ex *execution) settleSuccessfulResponse(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int) bool {
	usage := audioTokenUsage{}
	actualCost := ex.predictedCost
	costSnapshot := ex.costSnapshot
	pending := ex.pending
	if ex.scheme == audiopricing.SchemePerSecond {
		seconds, pendingReconciliation := ex.settleSeconds(raw)
		perUnit, err := ex.catalog.SecondMicroUSD(ex.providerForPricing(), ex.modelCandidatesForPricing())
		if err != nil {
			ex.abort(w, "pricing_unavailable", 0)
			writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
			return false
		}
		actualCost, costSnapshot, pending, err = ex.perUnitCost(seconds, perUnit)
		if err != nil {
			ex.abort(w, "pricing_unavailable", 0)
			writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
			return false
		}
		pending = pending || pendingReconciliation
	}
	if ex.scheme == audiopricing.SchemeToken {
		parsed, ok := parseTokenUsage(raw)
		if !ok {
			ex.observeChannelError(res.StatusCode)
			ex.abort(w, "usage_missing", 0)
			writeJSONError(w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, clienterr.MessageFor(clienterr.CodeCanonicalResponseError))
			return false
		}
		var err error
		usage = parsed
		actualCost, costSnapshot, pending, err = ex.tokenCost(parsed)
		if err != nil {
			ex.abort(w, "pricing_unavailable", int64(parsed.InputTokens))
			writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
			return false
		}
	}
	ex.observeSuccess(res)
	sbctx, scancel := ex.billingCtx()
	defer scancel()
	if _, err := ex.d.Settler.Settle(sbctx, ex.settleRequest(usage, actualCost, costSnapshot, attemptSeq, pending)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeSettleError, clienterr.MessageFor(clienterr.CodeSettleError))
		return false
	}
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		if ex.endpoint == audioEndpointSpeech {
			w.Header().Set("Content-Type", "application/octet-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
	return true
}

func (ex *execution) settleSeconds(raw []byte) (decimal.Decimal, bool) {
	if duration, ok := providerDuration(raw); ok {
		return duration, false
	}
	if ex.estimatedDuration.Precise {
		return ex.estimatedDuration.Seconds, false
	}
	return ex.estimatedDuration.Seconds, true
}

func providerDuration(raw []byte) (decimal.Decimal, bool) {
	var body struct {
		Duration json.RawMessage `json:"duration"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Duration) == 0 {
		return decimal.Zero, false
	}
	value, err := parseJSONDecimal(body.Duration)
	if err != nil || !value.GreaterThan(decimal.Zero) {
		return decimal.Zero, false
	}
	return value, true
}

func parseJSONDecimal(raw json.RawMessage) (decimal.Decimal, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return decimal.NewFromString(strings.TrimSpace(s))
	}
	return decimal.NewFromString(strings.TrimSpace(string(raw)))
}

func readUpstreamBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxUpstreamBodyBytes {
		return nil, fmt.Errorf("audio upstream response exceeds %d bytes", maxUpstreamBodyBytes)
	}
	return raw, nil
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
