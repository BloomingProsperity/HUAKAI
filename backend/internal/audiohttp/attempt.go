package audiohttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/audiopricing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/relaybody"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func (ex *execution) selectAccount(w http.ResponseWriter, attemptSeq int) bool {
	sel, err := ex.d.Selector.Select(ex.ctx, pool.SelectionRequest{
		TenantID:         ex.ident.TenantID,
		UserID:           ex.ident.UserID,
		APIKeyID:         ex.ident.APIKeyID,
		PoolGroupID:      ex.attempt.PoolGroupID,
		RequestedModel:   ex.req.Model,
		ModelCooldownKey: ex.upstreamModelID,
		ProtocolFamily:   ex.resolved.ProtocolFamily,
		EndpointFamily:   endpointFamilyAudio,
		ClaimID:          ex.reserveRes.ClaimID,
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

func (ex *execution) dispatchAndSettle(w http.ResponseWriter, attemptSeq int) attemptOutcome {
	// 把出站 body 的 model 改写成解析后的上游 id(JSON/multipart 皆可);multipart 会带新 boundary。
	inboundBody, inboundCT, _ := relaybody.RewriteModel(ex.body, ex.contentType, ex.upstreamModelID)
	startedAt := time.Now()
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
	defer func() {
		if res.Close != nil {
			_ = res.Close()
		}
	}()
	return ex.finishUpstreamResponse(w, res, attemptSeq, startedAt)
}

func (ex *execution) finishUpstreamResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int, startedAt time.Time) attemptOutcome {
	if res.StatusCode >= 200 && res.StatusCode < 300 && ex.endpoint == audioEndpointSpeech {
		ex.settleAndStreamSpeech(w, res, attemptSeq, startedAt)
		return attemptOutcome{Done: true}
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
	if strings.TrimSpace(string(raw)) == "" {
		ex.observeChannelError(startedAt, res.StatusCode)
		ex.abort(w, "upstream_empty_response", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse))
		return attemptOutcome{Done: true}
	}
	ex.observeSuccess(startedAt, res)
	_ = ex.settleSuccessfulResponse(w, res, raw, attemptSeq)
	return attemptOutcome{Done: true}
}

func (ex *execution) settleAndStreamSpeech(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int, startedAt time.Time) bool {
	first := make([]byte, 1)
	n, err := res.UpstreamReader.Read(first)
	if err == io.EOF {
		ex.observeChannelError(startedAt, res.StatusCode)
		ex.abort(w, "upstream_empty_response", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse))
		return false
	}
	if err != nil {
		ex.observeChannelError(startedAt, res.StatusCode)
		ex.abort(w, clienterr.CodeUpstreamReadError, 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
		return false
	}
	ex.observeSuccess(startedAt, res)
	// 重定价必须在写响应头之前:预扣用协议族 vendor 估的 predictedCost,选号后
	// 真账号平台(providerForPricing 此刻已含 accInfo.Platform)可能价不同,沿用
	// 预估会误扣/少收(与 per-second/token 在 settle 重算对称)。算价失败 → abort
	// 不计费,此刻响应未写可回 JSON 错误。
	actualCost, costSnapshot, pending, err := ex.charCost()
	if err != nil {
		ex.abort(w, "pricing_unavailable", 0)
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.WriteHeader(http.StatusOK)
	if n > 0 {
		if _, werr := w.Write(first[:n]); werr != nil {
			// 交付失败 → abort 不扣费(响应头已发,无法再返回 JSON 错误)。
			ex.abort(w, "client_delivery_failed", 0)
			return false
		}
	}
	if _, cerr := io.Copy(w, res.UpstreamReader); cerr != nil {
		ex.abort(w, "client_delivery_failed", 0)
		return false
	}
	// 音频完整交付后才结算,避免二进制断流误扣费(F1);结算走 billingCtx 防客户端断连取消(F2)。
	bctx, cancel := ex.billingCtx()
	defer cancel()
	req := ex.settleRequest(audioTokenUsage{}, actualCost, costSnapshot, attemptSeq, pending)
	if err := ex.settleDeliveredResponse(bctx, req); err != nil {
		return false
	}
	return true
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
	decision, classification, err := gateway.ClassifyAttemptHTTPError(
		res.StatusCode,
		res.Headers,
		raw,
		ex.classificationProvider(),
	)
	if err != nil {
		decision = gateway.AttemptRetryDecision{
			ClientStatus: http.StatusBadGateway,
			AbortReason:  "upstream_error",
		}
	}
	return &attemptFailure{Decision: decision, Classification: classification}
}

func (ex *execution) classificationProvider() string {
	if strings.EqualFold(strings.TrimSpace(ex.resolved.ProtocolFamily), "bedrock_invoke") {
		return "bedrock"
	}
	return ex.accInfo.Platform
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
	ex.catalog = nil
	ex.scheme = ""
	ex.predictedCost = decimal.Zero
	ex.costSnapshot = ""
	ex.pending = false
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
