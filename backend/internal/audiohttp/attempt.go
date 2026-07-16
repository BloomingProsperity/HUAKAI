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
		Vendor:              pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
		UserGroup:           ex.ident.UserGroup,
		SelectionMode:       ex.attempt.SelectionMode,
		BindingID:           ex.attempt.BindingID,
		MaxParallelRequests: ex.attempt.MaxParallelRequests,
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
		failure := fallbackexec.DispatchFailure(err)
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return attemptOutcome{failure: failure}
	}
	if res == nil || res.UpstreamReader == nil {
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
		failure := fallbackexec.UpstreamFailure(res.StatusCode, res.Headers, raw, ex.accInfo.Platform)
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
	// 音频完整交付后才结算,避免二进制断流误扣费(F1);结算走 billingCtx 防客户端断连取消(F2)。
	bctx, cancel := ex.billingCtx()
	defer cancel()
	if _, err := ex.d.Settler.Settle(bctx, ex.settleRequest(audioTokenUsage{}, actualCost, costSnapshot, attemptSeq, pending)); err != nil {
		// 交付后结算失败:响应已发出,无法回 500;响亮告警让对账可见(防静默漏记)。
		ex.logSettleAfterDeliveryFailure(err)
		return attemptOutcome{done: true}
	}
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
