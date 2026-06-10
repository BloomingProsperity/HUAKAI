package embeddingshttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
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
		EndpointFamily:   endpointFamilyEmbeddings,
		ClaimID:          ex.reserveRes.ClaimID,
		AttemptSeq:       attemptSeq,
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

// embedAttemptDone = 成功交付或已写终态错误;embedAttemptRetryable = 投递前网络层失败,
// claim 已 abort、未写响应,可换账号重试。
const (
	embedAttemptDone = iota
	embedAttemptRetryable
)

func (ex *execution) dispatchAndSettle(w http.ResponseWriter, attemptSeq int) int {
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    upstreamEmbeddingsPath,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.body,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	})
	if err != nil {
		ex.abort(w, "upstream_dispatch_error", 0)
		return embedAttemptRetryable
	}
	if res == nil || res.UpstreamReader == nil {
		ex.abort(w, "upstream_empty_response", 0)
		return embedAttemptRetryable
	}
	defer func() {
		if res.Close != nil {
			_ = res.Close()
		}
	}()
	_ = ex.finishUpstreamResponse(w, res, attemptSeq)
	return embedAttemptDone
}

func (ex *execution) finishUpstreamResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int) bool {
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	if readErr != nil {
		ex.abort(w, clienterr.CodeUpstreamReadError, 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
		return false
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		decision, _, err := gateway.ClassifyAttemptHTTPError(res.StatusCode, res.Headers, raw, ex.accInfo.Platform)
		reason := decision.AbortReason
		if err != nil || reason == "" {
			reason = "upstream_error"
		}
		ex.abort(w, reason, 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError))
		return false
	}
	return ex.settleSuccessfulResponse(w, res, raw, attemptSeq)
}

func (ex *execution) settleSuccessfulResponse(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int) bool {
	promptTokens, ok := promptTokens(raw)
	if !ok {
		ex.abort(w, "usage_missing", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, clienterr.MessageFor(clienterr.CodeCanonicalResponseError))
		return false
	}
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
