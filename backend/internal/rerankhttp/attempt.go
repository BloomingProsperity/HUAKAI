package rerankhttp

import (
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	fallbackexec "github.com/BloomingProsperity/HUAKAI/internal/bindingfallback/executor"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
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
		EndpointFamily:      endpointFamilyRerank,
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
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    upstreamRerankPath,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.body,
		Account:         ex.accInfo,
		Credential:      ex.cred,
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

func (ex *execution) settleSuccessfulResponse(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int) bool {
	sbctx, scancel := ex.billingCtx()
	defer scancel()
	if _, err := ex.d.Settler.Settle(sbctx, ex.settleRequest(ex.costSnapshot, attemptSeq)); err != nil {
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
