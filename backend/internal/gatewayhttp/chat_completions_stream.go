package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const (
	headerHUAKAIAuditLedgerID       = "X-HUAKAI-Ledger-ID"
	headerHUAKAIAuditLedgerDLQRef   = "X-HUAKAI-Ledger-DLQ-Ref"
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
	ex.cacheKey, err = ex.l2CacheKeyForModel(ex.upstreamModelID)
	if err != nil {
		if ex.reserveRes != nil {
			if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "cache_key_error", ex.requestID, 0); abortErr != nil {
				setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
			}
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadRequest, clienterr.CodeCacheKeyError, err)
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

func (ex *chatExecution) l2CacheKeyForModel(model string) (string, error) {
	key, _, err := l2cache.BuildKey(l2cache.KeyInput{
		TenantID:       ex.ident.TenantID,
		Vendor:         ex.cacheVendor,
		Model:          model,
		EndpointFamily: ex.d.effectiveEndpointFamily(),
		Body:           ex.body,
	})
	return key, err
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
		AttemptSeq:        ex.activeAttemptSeq(),
	}
}

func (ex *chatExecution) handleStreamingResponse(w http.ResponseWriter) {
	_ = ex.executeStreamingAttempt(w)
}

func (ex *chatExecution) executeStreamingAttempt(w http.ResponseWriter) attemptOutcome {
	outcome := ex.baseAttemptOutcome()
	upstreamAttemptStartedAt := time.Now()
	inboundBody := ex.body
	var clientAdapter proto.ClientAdapter
	if ex.needsStreamingHCSFTranslation() {
		var ok bool
		inboundBody, clientAdapter, ok = ex.translatedStreamingInboundBody(w)
		if !ok {
			return markAttemptOutcomeDelivered(outcome)
		}
	}
	dispatchRes, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     inboundBody,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	})
	if err != nil {
		classification, _ := gateway.Classify(0, nil, []byte(err.Error()), ex.accInfo.Platform)
		decision := gateway.ClassifyAttemptDispatchError(err)
		if decision.AbortReason == "" {
			decision.AbortReason = "upstream_dispatch_error"
		}
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), 0, time.Since(upstreamAttemptStartedAt), ex.requestID, nil)
		}
		outcome.Failure = degradeFailureIfAbortFailed(ex.ctx, ex.requestID, classifiedFailureFromDecision(clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, err), abortErr)
		return outcome
	}
	if dispatchRes == nil || dispatchRes.UpstreamReader == nil {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_empty_response", ex.requestID, 0)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, 0, time.Since(upstreamAttemptStartedAt), ex.requestID, nil)
		}
		failure := retryableLocalAttemptFailure(http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse), "upstream_empty_response", gateway.UpstreamError5xx, nil)
		outcome.Failure = degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
		return outcome
	}
	defer closeDispatchResult(dispatchRes)
	if dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300 {
		outcome.Failure = ex.classifyStreamingUpstreamFailure(dispatchRes, upstreamAttemptStartedAt)
		return outcome
	}
	deliveryStarted, failure := ex.forwardSSEAndSettle(w, dispatchRes, upstreamAttemptStartedAt, clientAdapter)
	outcome = ex.baseAttemptOutcome()
	outcome.DeliveryStarted = deliveryStarted
	if failure != nil && !deliveryStarted {
		outcome.Failure = failure
		outcome.UsageDraft = failureUsageDraft(failure)
		return outcome
	}
	outcome.Success = &attemptSuccess{StatusCode: http.StatusOK, Streamed: true}
	return outcome
}

func (ex *chatExecution) classifyStreamingUpstreamFailure(dispatchRes *gateway.DispatchResult, startedAt time.Time) *classifiedAttemptFailure {
	errBody, readErr := io.ReadAll(io.LimitReader(dispatchRes.UpstreamReader, 1<<20))
	if readErr != nil {
		errBody = []byte(readErr.Error())
	}
	decision, classification, classifyErr := gateway.ClassifyAttemptHTTPError(dispatchRes.StatusCode, dispatchRes.Headers, errBody, ex.accInfo.Platform)
	if classifyErr != nil {
		classification, _ = gateway.Classify(dispatchRes.StatusCode, dispatchRes.Headers, errBody, ex.accInfo.Platform)
		decision = gateway.AttemptRetryDecision{
			ClientStatus: clientStatusForUpstreamError(dispatchRes.StatusCode, classification.Class),
			AbortReason:  "upstream_error",
		}
	}
	abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0)
	if ex.healthKeyOK {
		recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromClassification(dispatchRes.StatusCode, classification), dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, rateLimitResetFromClassification(classification, time.Now()))
	}
	failure := classifiedFailureFromDecision("", clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, nil)
	return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
}

func (ex *chatExecution) forwardSSEAndSettle(w http.ResponseWriter, dispatchRes *gateway.DispatchResult, startedAt time.Time, clientAdapter proto.ClientAdapter) (bool, *classifiedAttemptFailure) {
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
	if streamForwarder.AuditLedgerDLQ == nil {
		streamForwarder.AuditLedgerDLQ = ex.d.AuditLedgerDLQ
	}
	if streamForwarder.Signer == nil {
		streamForwarder.Signer = ex.d.Signer
	}
	if clientAdapter != nil {
		streamForwarder.ClientAdapter = clientAdapter
	}
	var ledgerResult auditledger.AuditLedgerResult
	streamForwarder.LedgerCallback = func(result auditledger.AuditLedgerResult) {
		ledgerResult = result
		writeStreamingLedgerTrailers(w.Header(), result)
	}
	tracker := newDeliveryTracker(w)
	forwardWriter := http.ResponseWriter(tracker)
	var replayCapture *streamingIdempotencyReplayCaptureWriter
	if ex.shouldCaptureStreamingIdempotencyReplay() {
		replayCapture = newStreamingIdempotencyReplayCaptureWriter(tracker, maxIdempotencyReplayBodyBytes)
		forwardWriter = replayCapture
	}
	draft, fwdErr := streamForwarder.Forward(ex.ctx, dispatchRes.UpstreamReader, forwardWriter, ex.forwardReq)
	streamAttempt := billing.AttemptFromGatewayDraft(true, draft)
	if fwdErr != nil {
		logInternalError(ex.ctx, ex.requestID, clienterr.CodeForwardFailed, fwdErr)
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
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ex.ctx), 30*time.Second)
	defer cancel()
	ledgerFailClosed := auditLedgerProductionMode() && !streamingAuditLedgerResultAllowsSettle(ledgerResult)
	if ledgerFailClosed {
		streamAttempt.State = billing.StreamStateFailed
		streamAttempt.StreamTerminatedReason = "audit_ledger_error"
		streamAttempt = streamAttempt.Normalized()
	}
	writeStreamBillingHeaders(w.Header(), streamAttempt)
	// settle 条件三选一: 可计费 / 已向客户端交付内容 / 用量歧义需 audit 对账。
	// 仅"上游真零交付"(非计费 且 零交付 且 非 AmbiguousUsage) 才 abort —— 对齐
	// Owner 2026-05-20 计费策略,且避免 abort 已交付内容的流导致重试重复交付。
	settle := streamAttempt.State.Chargeable() ||
		streamAttempt.DeliveredTokenCount > 0 ||
		draft.EndClass == gateway.AmbiguousUsage
	var streamAbortErr error
	if settle && !ledgerFailClosed {
		event := ex.streamingCompletionEvent(draft, streamAttempt, ledgerResult)
		if _, err := settleCompletion(settleCtx, ex.d, event); err != nil {
			logInternalError(settleCtx, ex.requestID, clienterr.CodeSettleFailed, err)
		} else if replayCapture != nil && !replayCapture.overLimit() {
			ex.recordStreamingIdempotencyReplay(ex.reserveRes.ClaimID, replayCapture.statusCode(), replayCapture.body())
		}
	} else {
		reason := streamAttempt.StreamTerminatedReason
		if ledgerFailClosed {
			reason = "audit_ledger_error"
		} else if reason == "" {
			reason = "stream_no_billable_delivery"
		}
		observedInputTokens := ex.abortObservedInputTokens(draft)
		streamAbortErr = ex.d.Settler.Abort(settleCtx, ex.ident.TenantID, ex.reserveRes.ClaimID, reason, ex.requestID, observedInputTokens)
		if streamAbortErr != nil {
			logInternalError(settleCtx, ex.requestID, clienterr.CodeAbortFailed, streamAbortErr)
		}
	}
	deliveryStarted := tracker.started()
	if fwdErr != nil && !deliveryStarted {
		reason := streamAttempt.StreamTerminatedReason
		if reason == "" {
			reason = "upstream_timeout"
		}
		status := http.StatusServiceUnavailable
		if draft.EndClass == gateway.UpstreamError5xx {
			status = http.StatusBadGateway
		}
		failure := retryableLocalAttemptFailure(status, clienterr.CodeStreamForwardError, clienterr.MessageFor(clienterr.CodeStreamForwardError), reason, draft.EndClass, fwdErr)
		failure.EndClass = draft.EndClass
		return false, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, streamAbortErr)
	}
	return deliveryStarted, nil
}

func streamingAuditLedgerResultAllowsSettle(result auditledger.AuditLedgerResult) bool {
	return result.Validate(true) == nil
}

func failureUsageDraft(failure *classifiedAttemptFailure) gateway.UsageRecordDraft {
	if failure == nil {
		return gateway.UsageRecordDraft{}
	}
	return gateway.UsageRecordDraft{EndClass: failure.EndClass}
}

func (ex *chatExecution) abortObservedInputTokens(draft gateway.UsageRecordDraft) int64 {
	if draft.TokensInput <= 0 {
		return 0
	}
	if ex.streamInputOnlyInterruptedPolicy != billing.StreamInputOnlyInterruptedPolicyNoBillRecord {
		return 0
	}
	return int64(draft.TokensInput)
}

func (ex *chatExecution) needsStreamingHCSFTranslation() bool {
	if ex.clientProtocol == "" {
		return false
	}
	cp := string(ex.clientProtocol)
	fam := ex.resolved.ProtocolFamily
	if cp == fam {
		return false
	}
	// bedrock_invoke 已通过 AutoTranslateAnthropicAPIBody 在 PassthroughAdapter
	// 里把 anthropic_messages 客户端 body 自动转 Bedrock invoke body, 不需要
	// 也不应在此再走 HCSF (MarshalToProviderRequest 不支持 bedrock_invoke)
	if cp == "anthropic_messages" && fam == "bedrock_invoke" {
		return false
	}
	return true
}

func (ex *chatExecution) translatedStreamingInboundBody(w http.ResponseWriter) ([]byte, proto.ClientAdapter, bool) {
	clientAdapter, err := ex.streamingClientAdapter()
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "streaming_adapter_unregistered", ex.requestID, 0); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusServiceUnavailable, clienterr.CodeStreamingAdapterUnregistered, err)
		return nil, nil, false
	}
	seed := requestMetaSeed(ex.r, ex.ident, ex.clientProtocol, ex.resolved.ProtocolFamily, ex.routeID, ex.requestID, ex.acquiredAccountID, ex.acquisitionToken)
	seedCtx := proto.ContextWithRequestMetaSeed(ex.ctx, seed)
	canonicalReq, _, err := clientAdapter.RequestToCanonical(seedCtx, ex.body)
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "invalid_request_body", ex.requestID, 0); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadRequest, clienterr.CodeInvalidRequestBody, err)
		return nil, nil, false
	}
	if canonicalReq == nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "invalid_request_body", ex.requestID, 0); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeInvalidRequestBody, clienterr.MessageFor(clienterr.CodeInvalidRequestBody))
		return nil, nil, false
	}
	enrichCanonicalRequestMeta(canonicalReq, ex.upstreamModelID, ex.accInfo.Platform, ex.idempotencyHeader, ex.promptHash)
	canonicalReq.RequestMeta.EndpointFamily = ex.resolved.ProtocolFamily
	setAccountingModelRequested(canonicalReq, ex.req.Model)
	setAccountingModelRouteDecided(canonicalReq, ex.forwardReq.Model)
	gateway.ApplyForwardRequestHopChain(canonicalReq, ex.forwardReq)

	body, err := streamingProviderRequestBody(canonicalReq, ex.resolved.ProtocolFamily)
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "streaming_translation_not_supported", ex.requestID, 0); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusNotImplemented, clienterr.CodeStreamingTranslationUnsupported, err)
		return nil, nil, false
	}
	return body, canonicalEventPointerClientAdapter{inner: clientAdapter}, true
}

func (ex *chatExecution) streamingClientAdapter() (proto.ClientAdapter, error) {
	if ex.clientAdapter != nil {
		return ex.clientAdapter, nil
	}
	if ex.clientProtocol == "" {
		return nil, errors.New("streaming client protocol not inferred")
	}
	clientAdapter, ok := proto.DefaultClientAdapterRegistry().Lookup(ex.clientProtocol)
	if !ok {
		return nil, fmt.Errorf("client adapter not registered for protocol %q", ex.clientProtocol)
	}
	return clientAdapter, nil
}

func streamingProviderRequestBody(env *proto.HCSF, family string) ([]byte, error) {
	body, err := gateway.MarshalToProviderRequest(env, family)
	if err != nil {
		return nil, err
	}
	body, err = injectStreamingRequestControls(body, env, family)
	if err != nil {
		return nil, err
	}
	return forceStreamingRequest(body)
}

func injectStreamingRequestControls(raw []byte, env *proto.HCSF, family string) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	c := env.RequestControls
	if c.MaxTokens != nil {
		if family == "openai_responses" {
			body["max_output_tokens"] = *c.MaxTokens
		} else {
			body["max_tokens"] = *c.MaxTokens
		}
	}
	if c.Temperature != nil {
		body["temperature"] = *c.Temperature
	}
	if c.TopP != nil {
		body["top_p"] = *c.TopP
	}
	if c.ParallelToolCalls != nil && family != "anthropic_messages" {
		body["parallel_tool_calls"] = *c.ParallelToolCalls
	}
	if len(c.StopSequences) > 0 && family == "anthropic_messages" {
		body["stop_sequences"] = c.StopSequences
	} else if len(c.Stop) > 0 {
		body["stop"] = c.Stop
	} else if len(c.StopSequences) > 0 {
		body["stop"] = c.StopSequences
	}
	if len(c.ToolChoice) > 0 {
		body["tool_choice"] = streamingRawJSONValue(c.ToolChoice)
	}
	if len(c.Tools) > 0 {
		body["tools"] = streamingControlTools(family, c.Tools)
	}
	if c.ResponseFormat != nil {
		rf := map[string]any{"type": c.ResponseFormat.Type}
		if len(c.ResponseFormat.Schema) > 0 {
			rf["schema"] = streamingRawJSONValue(c.ResponseFormat.Schema)
		}
		if c.ResponseFormat.Strict != nil {
			rf["strict"] = *c.ResponseFormat.Strict
		}
		if family == "openai_responses" {
			body["text"] = map[string]any{"format": rf}
		} else if family == "openai_chat" {
			body["response_format"] = rf
		}
	}
	return json.Marshal(body)
}

func forceStreamingRequest(raw []byte) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	body["stream"] = true
	return json.Marshal(body)
}

func streamingControlTools(family string, tools []proto.CanonicalTool) []any {
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		schema := streamingRawJSONValue(tool.InputSchema)
		switch family {
		case "openai_chat":
			out = append(out, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": schema}})
		case "openai_responses":
			out = append(out, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": schema})
		default:
			out = append(out, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": schema})
		}
	}
	return out
}

func streamingRawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{}
	}
	return v
}

type canonicalEventPointerClientAdapter struct {
	inner proto.ClientAdapter
}

func (a canonicalEventPointerClientAdapter) RequestToCanonical(ctx context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return a.inner.RequestToCanonical(ctx, raw)
}

func (a canonicalEventPointerClientAdapter) CanonicalToClientResponse(ctx context.Context, canonical *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return a.inner.CanonicalToClientResponse(ctx, canonical)
}

func (a canonicalEventPointerClientAdapter) CanonicalEventToClientChunk(ctx context.Context, canonicalEvt any, state any) ([][]byte, []proto.ProtocolLossEntry, error) {
	if evt, ok := canonicalEvt.(proto.CanonicalEvent); ok {
		canonicalEvt = &evt
	}
	return a.inner.CanonicalEventToClientChunk(ctx, canonicalEvt, state)
}

func (a canonicalEventPointerClientAdapter) FinalizeClientStream(ctx context.Context, state any) ([][]byte, error) {
	return a.inner.FinalizeClientStream(ctx, state)
}

func (ex *chatExecution) streamingCompletionEvent(draft gateway.UsageRecordDraft, streamAttempt billing.Attempt, ledgerResult auditledger.AuditLedgerResult) eventbus.RequestCompletionEvent {
	actualCost, err := ex.actualCompletionCost(usageFromDraft(draft))
	if err != nil {
		draft.PendingReconciliation = true
		actualCost = decimal.Zero
	}
	draft.ActualCost = actualCost
	return eventbus.RequestCompletionEvent{
		ID:                        ex.requestID,
		TenantID:                  ex.ident.TenantID,
		ClaimID:                   ex.reserveRes.ClaimID,
		AccountID:                 ex.acquiredAccountID,
		RequestID:                 ex.requestID,
		EndpointFamily:            ex.d.effectiveEndpointFamily(),
		RequestedModel:            ex.req.Model,
		UpstreamModel:             ex.upstreamModelID,
		PayloadHash:               ex.payloadHash,
		RawBodyHash:               bodyHash(ex.body),
		RedactedBodyRef:           redactedBodyRef(ex.body),
		AuditLedgerID:             ledgerID(ledgerResult),
		AuditLedgerDLQRef:         ledgerDLQRef(ledgerResult),
		AuditSignatureFingerprint: ledgerFingerprint(ledgerResult),
		SettleRequest: billing.SettleRequest{
			ClaimID:           ex.reserveRes.ClaimID,
			AccountID:         ex.acquiredAccountID,
			AcquisitionToken:  ex.acquisitionToken,
			TenantID:          ex.ident.TenantID,
			APIKeyID:          ex.ident.APIKeyID,
			UserID:            ex.ident.UserID,
			ProviderAccountID: ex.acquiredAccountID,
			AttemptSeq:        int32(ex.activeAttemptSeq()),
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
		Metadata:                  routeMetadata(ex.routeID),
	}
}

func declareStreamBillingTrailers(h http.Header) {
	if h == nil {
		return
	}
	h.Add("Trailer", headerHUAKAIStreamState)
	h.Add("Trailer", headerHUAKAIDeliveredTokens)
	h.Add("Trailer", headerHUAKAIAuditLedgerID)
	h.Add("Trailer", headerHUAKAIAuditLedgerDLQRef)
}

func writeStreamBillingHeaders(h http.Header, attempt billing.Attempt) {
	if h == nil {
		return
	}
	attempt = attempt.Normalized()
	h.Set(headerHUAKAIStreamState, attempt.State.String())
	h.Set(headerHUAKAIDeliveredTokens, strconv.FormatInt(attempt.DeliveredTokenCount, 10))
}

func writeStreamingLedgerTrailers(h http.Header, result auditledger.AuditLedgerResult) {
	if h == nil {
		return
	}
	switch result.State {
	case auditledger.LedgerResultStatePersisted:
		if result.LedgerID != "" {
			h.Set(headerHUAKAIAuditLedgerID, result.LedgerID)
		}
	case auditledger.LedgerResultStateDeferred:
		if result.DLQRef != "" {
			h.Set(headerHUAKAIAuditLedgerDLQRef, result.DLQRef)
		}
	}
}
