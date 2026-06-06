package completionshttp

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

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

const (
	attemptDone = iota
	attemptRetryable
)

func (ex *execution) dispatchCompletionsAndSettle(w http.ResponseWriter, attemptSeq int) int {
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    ex.upstreamPath,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.body,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	})
	if err != nil {
		ex.abort(w, "upstream_dispatch_error", 0)
		return attemptRetryable
	}
	if res == nil || res.UpstreamReader == nil {
		ex.abort(w, "upstream_empty_response", 0)
		return attemptRetryable
	}
	defer closeDispatchResult(res)
	_ = ex.finishCompletionsResponse(w, res, attemptSeq)
	return attemptDone
}

func (ex *execution) finishCompletionsResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int) bool {
	if ex.req.Stream || strings.Contains(strings.ToLower(res.Headers.Get("Content-Type")), "text/event-stream") {
		return ex.finishStreamingResponse(w, res, attemptSeq)
	}
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	if readErr != nil {
		ex.abort(w, clienterr.CodeUpstreamReadError, 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
		return false
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		ex.abort(w, upstreamAbortReason(res.StatusCode, res.Headers, raw), 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError))
		return false
	}
	return ex.settleAndWriteJSON(w, res, raw, attemptSeq)
}

func (ex *execution) settleAndWriteJSON(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int) bool {
	usage, ok := usageFromJSON(raw)
	if !ok {
		ex.abort(w, "usage_missing", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, clienterr.MessageFor(clienterr.CodeCanonicalResponseError))
		return false
	}
	cost, err := ex.actualCost(usage)
	if err != nil {
		ex.abort(w, "pricing_unavailable", int64(usage.PromptTokens))
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	if _, err := ex.d.Settler.Settle(ex.ctx, ex.settleRequest(usage, cost, attemptSeq, false)); err != nil {
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

func (ex *execution) finishStreamingResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int) bool {
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, readErr := readUpstreamBody(res.UpstreamReader)
		if readErr != nil {
			ex.abort(w, clienterr.CodeUpstreamReadError, 0)
			writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
			return false
		}
		ex.abort(w, upstreamAbortReason(res.StatusCode, res.Headers, raw), 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError))
		return false
	}

	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.WriteHeader(http.StatusOK)

	var copied bytes.Buffer
	if err := streamAndCapture(w, res.UpstreamReader, &copied); err != nil {
		ex.abort(w, clienterr.CodeUpstreamReadError, 0)
		return false
	}
	usage, ok := usageFromSSE(copied.Bytes())
	if !ok {
		usage = completionUsage{PromptTokens: ex.inputEstimate}
	}
	cost, err := ex.actualCost(usage)
	if err != nil {
		ex.abort(w, "pricing_unavailable", int64(usage.PromptTokens))
		return false
	}
	if !ok {
		cost.PendingReconciliation = true
		if cost.CostSnapshot == "" {
			cost.CostSnapshot = "pending_reconciliation=stream_usage_missing"
		} else {
			cost.CostSnapshot += ";pending_reconciliation=stream_usage_missing"
		}
	}
	if _, err := ex.d.Settler.Settle(ex.ctx, ex.settleRequest(usage, cost, attemptSeq, true)); err != nil {
		w.Header().Set("X-Huakai-Settle-Failed", clienterr.CodeSettleError)
		return false
	}
	return true
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

func closeDispatchResult(res *gateway.DispatchResult) {
	if res != nil && res.Close != nil {
		_ = res.Close()
	}
}
