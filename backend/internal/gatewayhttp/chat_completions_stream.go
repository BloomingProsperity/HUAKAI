package gatewayhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

const (
	headerHUAKAIAuditLedgerID       = "X-HUAKAI-Ledger-ID"
	headerHUAKAIAuditVerify         = "X-HUAKAI-Verify"
	headerHUAKAIAuditSigFingerprint = "X-HUAKAI-Sig-Fingerprint"
	headerHUAKAIStreamState         = "X-HUAKAI-Stream-State"
	headerHUAKAIDeliveredTokens     = "X-HUAKAI-Delivered-Tokens"
)

func (ex *chatExecution) serveL2CacheIfAvailable(w http.ResponseWriter) (bool, bool) {
	if ex.d.ResponseCache == nil || ex.req.Stream {
		return false, true
	}
	var err error
	ex.cacheKey, _, err = l2cache.BuildKey(l2cache.KeyInput{
		TenantID: ex.ident.TenantID,
		Vendor:   ex.cacheVendor,
		Model:    ex.upstreamModelID,
		Body:     ex.body,
	})
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "cache_key_error", ex.requestID)
		writeJSONError(w, http.StatusBadRequest, "cache_key_error", err.Error())
		return false, false
	}
	if cached, ok := ex.d.ResponseCache.Get(ex.ctx, ex.cacheKey); ok {
		cachemetrics.ObserveL2Hit(ex.cacheVendor, ex.upstreamModelID)
		if serveL2CacheHit(ex.ctx, w, ex.r, ex.d, ex.cacheHitInput(cached)) {
			return true, false
		}
		ex.d.ResponseCache.Delete(ex.ctx, ex.cacheKey)
		syncL2SizeMetrics(ex.d.ResponseCache)
	}
	cachemetrics.ObserveL2Miss(ex.cacheVendor, ex.upstreamModelID)
	return false, true
}

func (ex *chatExecution) cacheHitInput(entry l2cache.Entry) l2CacheHitInput {
	return l2CacheHitInput{
		Entry:             entry,
		Ident:             ex.ident,
		ClientProtocol:    ex.clientProtocol,
		ProtocolFamily:    ex.resolved.ProtocolFamily,
		RouteID:           ex.routeID,
		RequestID:         ex.requestID,
		AccountID:         ex.acquiredAccountID,
		AcquisitionToken:  ex.acquisitionToken,
		PoolID:            fmt.Sprintf("%d", ex.attempt.PoolGroupID),
		UpstreamModelID:   ex.upstreamModelID,
		RequestedModel:    ex.req.Model,
		Provider:          ex.cacheVendor,
		IdempotencyHeader: ex.idempotencyHeader,
		PromptHash:        ex.promptHash,
		RequestStartedAt:  ex.startedAt,
		ReserveResult:     ex.reserveRes,
		SelectionResult:   ex.selRes,
		PlanSnapshot:      ex.plan.SnapshotVersion,
		PayloadHash:       ex.payloadHash,
	}
}

func (ex *chatExecution) handleStreamingResponse(w http.ResponseWriter) {
	upstreamAttemptStartedAt := time.Now()
	dispatchRes, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.body,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	})
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_dispatch_error", ex.requestID)
		classification, _ := gateway.Classify(0, nil, []byte(err.Error()), ex.accInfo.Platform)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), 0, time.Since(upstreamAttemptStartedAt), ex.requestID, nil)
		}
		writeNormalizedUpstreamError(w, http.StatusBadGateway, "upstream_dispatch_error", classification)
		return
	}
	if dispatchRes == nil || dispatchRes.UpstreamReader == nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_empty_response", ex.requestID)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, 0, time.Since(upstreamAttemptStartedAt), ex.requestID, nil)
		}
		writeJSONError(w, http.StatusBadGateway, "upstream_empty_response", "upstream returned no response body")
		return
	}
	defer closeDispatchResult(dispatchRes)
	if dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300 {
		ex.writeStreamingUpstreamError(w, dispatchRes, upstreamAttemptStartedAt)
		return
	}
	ex.forwardSSEAndSettle(w, dispatchRes, upstreamAttemptStartedAt)
}

func (ex *chatExecution) writeStreamingUpstreamError(w http.ResponseWriter, dispatchRes *gateway.DispatchResult, startedAt time.Time) {
	errBody, readErr := io.ReadAll(io.LimitReader(dispatchRes.UpstreamReader, 1<<20))
	if readErr != nil {
		errBody = []byte(readErr.Error())
	}
	classification, classifyErr := gateway.Classify(dispatchRes.StatusCode, dispatchRes.Headers, errBody, ex.accInfo.Platform)
	abortReason := "upstream_error"
	if classifyErr == nil && classification.Class != "" {
		abortReason = "upstream_" + string(classification.Class)
	}
	_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, abortReason, ex.requestID)
	if ex.healthKeyOK {
		recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromClassification(dispatchRes.StatusCode, classification), dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, rateLimitResetFromClassification(classification, time.Now()))
	}
	writeNormalizedUpstreamError(w, clientStatusForUpstreamError(dispatchRes.StatusCode, classification.Class), "upstream_error", classification)
}

func (ex *chatExecution) forwardSSEAndSettle(w http.ResponseWriter, dispatchRes *gateway.DispatchResult, startedAt time.Time) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if ex.d.ResponseCache != nil {
		w.Header().Set("X-HUAKAI-Cache-L2", "skip")
	}
	declareStreamBillingTrailers(w.Header())
	writeStreamBillingHeaders(w.Header(), billing.Attempt{State: billing.StreamStateInFlight})
	streamForwarder := *ex.d.Forwarder
	if streamForwarder.AuditLedger == nil {
		streamForwarder.AuditLedger = ex.d.AuditLedger
	}
	if streamForwarder.Signer == nil {
		streamForwarder.Signer = ex.d.Signer
	}
	streamForwarder.LedgerCallback = func(entryID, sigFingerprint string) {
		WriteHuakaiLedgerHeaders(w.Header(), ex.requestID, entryID, sigFingerprint)
	}
	draft, fwdErr := streamForwarder.Forward(ex.ctx, dispatchRes.UpstreamReader, w, ex.forwardReq)
	streamAttempt := billing.AttemptFromGatewayDraft(true, draft)
	writeStreamBillingHeaders(w.Header(), streamAttempt)
	if fwdErr != nil {
		w.Header().Set("X-Huakai-Forward-Error", fwdErr.Error())
		if ex.healthKeyOK {
			class := channelhealth.SignalChannelError
			if errors.Is(fwdErr, context.DeadlineExceeded) || os.IsTimeout(fwdErr) {
				class = channelhealth.SignalTimeout
			}
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, class, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil)
		}
	} else if ex.healthKeyOK {
		recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalSuccess, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil)
	}
	if _, err := settleCompletion(ex.ctx, ex.d, ex.streamingCompletionEvent(draft, streamAttempt)); err != nil {
		w.Header().Set("X-Huakai-Settle-Error", err.Error())
	}
}

func (ex *chatExecution) streamingCompletionEvent(draft gateway.UsageRecordDraft, streamAttempt billing.Attempt) eventbus.RequestCompletionEvent {
	actualCost := decimal.NewFromFloat(0.01)
	return eventbus.RequestCompletionEvent{
		ID:              ex.requestID,
		TenantID:        ex.ident.TenantID,
		ClaimID:         ex.reserveRes.ClaimID,
		AccountID:       ex.acquiredAccountID,
		RequestID:       ex.requestID,
		EndpointFamily:  ex.d.effectiveEndpointFamily(),
		RequestedModel:  ex.req.Model,
		UpstreamModel:   ex.upstreamModelID,
		PayloadHash:     ex.payloadHash,
		RawBodyHash:     bodyHash(ex.body),
		RedactedBodyRef: redactedBodyRef(ex.body),
		SettleRequest: billing.SettleRequest{
			ClaimID:           ex.reserveRes.ClaimID,
			AccountID:         ex.acquiredAccountID,
			AcquisitionToken:  ex.acquisitionToken,
			TenantID:          ex.ident.TenantID,
			APIKeyID:          ex.ident.APIKeyID,
			UserID:            ex.ident.UserID,
			ProviderAccountID: ex.acquiredAccountID,
			AttemptSeq:        1,
			RequestedModel:    ex.req.Model,
			UpstreamModel:     ex.upstreamModelID,
			Provider:          ex.cacheVendor,
			Stream:            true,
			ActualCost:        actualCost,
			Fingerprint:       ex.payloadHash,
			Draft:             draft,
			StreamAttempt:     &streamAttempt,
			SnapshotVersion:   ex.plan.SnapshotVersion,
		},
	}
}

func declareStreamBillingTrailers(h http.Header) {
	if h == nil {
		return
	}
	h.Add("Trailer", headerHUAKAIStreamState)
	h.Add("Trailer", headerHUAKAIDeliveredTokens)
}

func writeStreamBillingHeaders(h http.Header, attempt billing.Attempt) {
	if h == nil {
		return
	}
	attempt = attempt.Normalized()
	h.Set(headerHUAKAIStreamState, attempt.State.String())
	h.Set(headerHUAKAIDeliveredTokens, strconv.FormatInt(attempt.DeliveredTokenCount, 10))
}
