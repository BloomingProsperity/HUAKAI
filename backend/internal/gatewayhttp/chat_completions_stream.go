package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/trust"
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
			if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "cache_key_error", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
				setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
			}
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadRequest, clienterr.CodeCacheKeyError, err)
		return false, false
	}
	if cached, ok := ex.d.ResponseCache.Get(ex.ctx, ex.cacheKey); ok {
		if cached.TenantID != ex.ident.TenantID || cached.ScopeID != ex.cacheScopeID() {
			// 纵深防御: 物理 key 已把 TenantID + scope principal 哈希进去(cache/key.go), 正常绝不会
			// 取到别租户/别 principal 条目; 若发生说明 key 被弱化或缓存被污染 —— 绝不跨租户/跨
			// principal serve。删除该条目、记录, 当作 miss 走正常上游。
			logInternalError(ex.ctx, ex.requestID, "l2_cache_principal_mismatch",
				fmt.Errorf("cached entry tenant=%d scope_id=%d != request tenant=%d scope_id=%d", cached.TenantID, cached.ScopeID, ex.ident.TenantID, ex.cacheScopeID()))
			ex.d.ResponseCache.Delete(ex.ctx, ex.cacheKey)
			syncL2SizeMetrics(ex.d.ResponseCache)
		} else {
			cachemetrics.ObserveL2Hit(ex.cacheVendor, ex.upstreamModelID)
			if serveL2CacheHit(ex.ctx, w, ex.r, ex.d, ex.cacheHitInput(cached)) {
				return true, false
			}
			ex.d.ResponseCache.Delete(ex.ctx, ex.cacheKey)
			syncL2SizeMetrics(ex.d.ResponseCache)
		}
	}
	cachemetrics.ObserveL2Miss(ex.cacheVendor, ex.upstreamModelID)
	return false, true
}

func (ex *chatExecution) l2CacheKeyForModel(model string) (string, error) {
	key, _, err := l2cache.BuildKey(l2cache.KeyInput{
		TenantID:       ex.ident.TenantID,
		Scope:          l2cache.Scope(ex.d.effectiveCacheScope()),
		APIKeyID:       ex.ident.APIKeyID,
		UserID:         ex.ident.UserID,
		Vendor:         ex.cacheVendor,
		Model:          model,
		EndpointFamily: ex.d.effectiveEndpointFamily(),
		Body:           ex.body,
	})
	return key, err
}

// cacheScopeID 返回当前 scope 下用于 L2 缓存 principal 围栏的 id (tenant=0, apikey=APIKeyID, user=UserID)。
func (ex *chatExecution) cacheScopeID() int64 {
	switch l2cache.NormalizeScope(ex.d.effectiveCacheScope()) {
	case l2cache.ScopeAPIKey:
		return ex.ident.APIKeyID
	case l2cache.ScopeUser:
		return ex.ident.UserID
	default:
		return 0
	}
}

func (ex *chatExecution) cacheHitInput(entry l2cache.Entry) l2CacheHitInput {
	return l2CacheHitInput{
		Entry:             entry,
		Ident:             ex.ident,
		ClientProtocol:    ex.clientProtocol,
		ProtocolFamily:    ex.resolved.ProtocolFamily,
		RouteID:           ex.routeID,
		RequestID:         ex.requestID,
		ClientRequestID:   ex.clientRequestID,
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
	transportSelection := transportSelectionForDispatch(ex.accInfo, ex.resolved.ProtocolFamily)
	dispatchRes, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.upstreamInboundBody(inboundBody),
		Account:         transportSelection.account,
		Credential:      ex.cred,
		TransportMode:   transportSelection.mode,
	})
	if err != nil {
		classification, _ := gateway.Classify(0, nil, []byte(err.Error()), ex.accInfo.Platform)
		decision := gateway.ClassifyAttemptDispatchError(err)
		if decision.AbortReason == "" {
			decision.AbortReason = "upstream_dispatch_error"
		}
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0, ex.protocolLoss)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), 0, time.Since(upstreamAttemptStartedAt), ex.requestID, nil)
		}
		outcome.Failure = degradeFailureIfAbortFailed(ex.ctx, ex.requestID, classifiedFailureFromDecision(clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, err), abortErr)
		return outcome
	}
	if dispatchRes == nil || dispatchRes.UpstreamReader == nil {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_empty_response", ex.requestID, 0, ex.protocolLoss)
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
	recordModelCooldownOnUpstream404(ex.ctx, ex.d, ex.ident.TenantID, ex.acquiredAccountID, ex.upstreamModelID, dispatchRes.StatusCode, ex.requestID)
	abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0, ex.protocolLoss)
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
	// 告诉 nginx/ingress 等反代不要缓冲此 SSE 响应,否则它会攒着我们的逐块输出与 keepalive 心跳,
	// 反而把"连接活跃"信号挡掉,重新引入空闲超时问题。
	w.Header().Set("X-Accel-Buffering", "no")
	if ex.d.ResponseCache != nil {
		w.Header().Set("X-HUAKAI-Cache-L2", "skip")
	}
	trust.WriteResponseHeaders(w.Header(), trust.ResponseMetadata{
		Provider:  ex.forwardReq.Provider,
		Model:     ex.forwardReq.Model,
		RequestID: ex.requestID,
	}, auditledger.DisabledLedgerResult())
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
		writeStreamingLedgerTrailers(w.Header(), result, ex.requestID, ex.ident.TenantID)
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
		// post-delivery:forwardSSEAndSettle 已经把内容写给客户端,settle
		// 失败时通过 settleCompletionWithRecovery 把 RequestCompletionEvent
		// 转 settlementrecovery DLQ 持久化,worker 后续重 settle 防钱账丢失。
		if _, err := settleCompletionWithRecovery(settleCtx, ex.d, event, settlementrecovery.SourceStream); err != nil {
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
		streamAbortErr = ex.d.Settler.Abort(settleCtx, ex.ident.TenantID, ex.reserveRes.ClaimID, reason, ex.requestID, observedInputTokens, ex.protocolLoss)
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
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "streaming_adapter_unregistered", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusServiceUnavailable, clienterr.CodeStreamingAdapterUnregistered, err)
		return nil, nil, false
	}
	seed := requestMetaSeed(ex.r, ex.ident, ex.clientProtocol, ex.resolved.ProtocolFamily, ex.routeID, ex.requestID, ex.acquiredAccountID, ex.acquisitionToken)
	seedCtx := proto.ContextWithRequestMetaSeed(ex.ctx, seed)
	canonicalReq, protocolLosses, err := clientAdapter.RequestToCanonical(seedCtx, ex.body)
	ex.protocolLoss = protocolLossJSONFromEntries(protocolLosses)
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "invalid_request_body", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadRequest, clienterr.CodeInvalidRequestBody, err)
		return nil, nil, false
	}
	if canonicalReq == nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "invalid_request_body", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeInvalidRequestBody, clienterr.MessageFor(clienterr.CodeInvalidRequestBody))
		return nil, nil, false
	}
	enrichCanonicalRequestMeta(canonicalReq, ex.upstreamModelID, ex.accInfo.Platform, ex.idempotencyHeader, ex.sessionHash)
	canonicalReq.RequestMeta.EndpointFamily = ex.resolved.ProtocolFamily
	setAccountingModelRequested(canonicalReq, ex.req.Model)
	setAccountingModelRouteDecided(canonicalReq, ex.forwardReq.Model)
	gateway.ApplyForwardRequestHopChain(canonicalReq, ex.forwardReq)

	body, err := streamingProviderRequestBody(canonicalReq, ex.resolved.ProtocolFamily)
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "streaming_translation_not_supported", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
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
		// D5 raw-passthrough:同非流式逻辑(hcsf_graph_marshal_helpers.go 的
		// injectRequestControls)。Schema 存的是 inbound 原始 response_format /
		// text 整体,流式 marshal 必须 1:1 还原,不能再包 {"type":"raw","schema":...}
		// 让上游 4xx reject。
		if c.ResponseFormat.Type == "raw" && len(c.ResponseFormat.Schema) > 0 {
			switch family {
			case "openai_responses":
				body["text"] = streamingRawJSONValue(c.ResponseFormat.Schema)
			case "openai_chat":
				body["response_format"] = streamingRawJSONValue(c.ResponseFormat.Schema)
			}
		} else {
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
	usage := usageFromDraft(draft)
	actualCost, err := ex.actualCompletionCost(usage)
	if err != nil {
		draft.PendingReconciliation = true
		actualCost = completionCostBreakdown{}
		// 缺上游 usage（无任何 token 信号）但已交付内容：不把 DeliveredTokenCount 当 token 计费——
		// 它此处是内容帧数（canonicalDeliveredChunks）而非 token 数；细碎 tool_input/sub-token 分帧
		// 会使帧数 > 真实 token 数，按帧计费会向用户多收。保持 ActualCost=0 + pending + inferred，
		// 交由 settlementreconcile worker 宽限后零差额定稿（无权威 usage 会到达）。
		// 仅在确为缺 usage 时标 inferred：计费配置失败（rate table 缺失但有真实 token）不可标 inferred，
		// 否则 worker 会把真实请求零差额定稿成 $0（静默零计费）。
		// 不覆盖 Ambiguous：歧义用量（unknown termination 等）须保留歧义态留待真对账，
		// 不可降级成 inferred 而被宽限定稿。
		if reportedUsageMissing(usage) && draft.DeliveredTokenCount > 0 &&
			draft.UsageSource != gateway.UsageSourceAmbiguous {
			draft.UsageSource = gateway.UsageSourceInferred
		}
	}
	draft.ActualCost = actualCost.Total
	draft.CostSnapshot = actualCost.CostSnapshot
	draft.CacheCreationCost = actualCost.CacheCreationCost
	draft.CacheReadCost = actualCost.CacheReadCost
	// 流式 token 交叉校验(镜像非流 nonStreamingUsageDraft,审计-only,不改成本/usage_source):
	// forwarder 逐事件累加的可见输出估算(draft.EstimatedOutputTokens)与 reported OutputTokens
	// (扣除隐藏 reasoning)比对。估算为 0(未捕获可估内容)→ Unknown → 不降级。reasoning 文本
	// 流出但无 ReasoningTokens(Anthropic/Gemini thinking,folding 不可知)→ 跳过校验避免误报。
	// pending 与上方缺 usage 的 pending 取并集,不互相覆盖(S2-163-fu;review R2)。
	streamConfidence, streamPending := crossCheckAudit(draft.TokensOutput, draft.ReasoningTokens, draft.EstimatedOutputTokens, draft.EstimatedReasoningTokens, actualCost.Total.IsPositive())
	draft.ConfidenceScore = &streamConfidence
	if streamPending || actualCost.PendingReconciliation {
		draft.PendingReconciliation = true
	}
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
			ActualCost:        actualCost.Total,
			// 合并请求翻译损失(ex.protocolLoss)与流式逐事件损失(draft.StreamProtocolLoss);
			// 后者之前被 StreamForwarder 丢弃,只有初始(常为空)请求侧损失能到 settle(S1-025-fu item 4)。
			ProtocolLoss:        mergeProtocolLossWithEntries(ex.protocolLoss, draft.StreamProtocolLoss),
			Fingerprint:         ex.payloadHash,
			Draft:               draft,
			StreamAttempt:       &streamAttempt,
			EmitSchedulerOutbox: true,
			SnapshotVersion:     ex.plan.SnapshotVersion,
		},
		Metadata: completionMetadata(ex.routeID, ex.clientRequestID),
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
	h.Add("Trailer", headerHUAKAIAuditVerify)
	h.Add("Trailer", headerHUAKAIAuditSigFingerprint)
}

func writeStreamBillingHeaders(h http.Header, attempt billing.Attempt) {
	if h == nil {
		return
	}
	attempt = attempt.Normalized()
	h.Set(headerHUAKAIStreamState, attempt.State.String())
	h.Set(headerHUAKAIDeliveredTokens, strconv.FormatInt(attempt.DeliveredTokenCount, 10))
}

func writeStreamingLedgerTrailers(h http.Header, result auditledger.AuditLedgerResult, requestID string, tenantID int64) {
	if h == nil {
		return
	}
	switch result.State {
	case auditledger.LedgerResultStatePersisted:
		if result.LedgerID != "" {
			h.Set(headerHUAKAIAuditLedgerID, result.LedgerID)
		}
		if result.Fingerprint != "" {
			h.Set(headerHUAKAIAuditSigFingerprint, result.Fingerprint)
		}
		if result.LedgerID != "" && requestID != "" {
			query := url.Values{}
			query.Set("request_id", requestID)
			query.Set("ledger-id", result.LedgerID)
			if scopeRef := auditledger.TenantScopeRef(tenantID); scopeRef != "" {
				query.Set("tenant_scope_ref", scopeRef)
			}
			h.Set(headerHUAKAIAuditVerify, "/v1/audit/verify?"+query.Encode())
		}
	case auditledger.LedgerResultStateDeferred:
		if result.DLQRef != "" {
			h.Set(headerHUAKAIAuditLedgerDLQRef, result.DLQRef)
		}
	}
}
