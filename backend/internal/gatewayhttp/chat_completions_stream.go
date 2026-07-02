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
	"github.com/BloomingProsperity/HUAKAI/internal/mimicryidentity"
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

// identityRewrite 对 dispatch 专用 body 施加 R7 身份改写(默认关 + fail-open),
// 三条 dispatch 路径共用本方法。默认关/fail-open/external id 空跳过等语义全由
// mimicryidentity 子包保证(详见该包文档)。翻全局默认是 Owner-gated 二阶段。
func (ex *chatExecution) identityRewrite(dispatchBody []byte) []byte {
	if ex == nil || ex.r == nil {
		return dispatchBody
	}
	// 协议族门控:R7 改写写 metadata.user_id(Anthropic 专属语义),仅当上游族是
	// anthropic_messages 时 dispatchBody 才是 Anthropic 形、注入才合法。其余族
	// (OpenAI/Gemini/Bedrock)强注入会被上游拒(400/语义错配),故 fail-open 返回原 body。
	if ex.resolved.ProtocolFamily != "anthropic_messages" {
		return dispatchBody
	}
	return mimicryidentity.RewriteForDispatch(
		dispatchBody,
		ex.accInfo.AccountID,
		ex.accInfo.ExternalAccountID, // 空 → fail-open 不改写
		ex.clientSessionID,           // 参与 session 派生,避免同账号跨会话共用 upstream session
		mimicryidentity.ExtractClaudeCodeVersion(ex.r.UserAgent()),
	)
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
		// R7 身份改写(默认关 + fail-open,只动 dispatch 专用拷贝、不动 ex.body)。
		InboundBody:       ex.identityRewrite(ex.upstreamInboundBody(inboundBody)),
		BodyControls:      ex.activeDispatchBodyControls(),
		InboundBetaTokens: ex.clientBetaTokens(),
		Account:           transportSelection.account,
		Credential:        ex.cred,
		TransportMode:     transportSelection.mode,
		// 跨协议流式意图:非 gemini ingress 不注入 Extra["stream"],marshal 出的
		// gemini body 又无顶层 stream 字段,没有这条 gemini-shaped 上游会错选
		// 非流 :generateContent(评审 A4/A5 共识缺口)。
		ClientStreamIntent: ex.req.Stream,
	})
	if err != nil {
		classification, _ := gateway.Classify(0, nil, []byte(err.Error()), ex.accInfo.Platform)
		decision := gateway.ClassifyAttemptDispatchError(err)
		if decision.AbortReason == "" {
			decision.AbortReason = "upstream_dispatch_error"
		}
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0, ex.protocolLoss)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), 0, time.Since(upstreamAttemptStartedAt), ex.requestID, nil, 0)
		}
		outcome.Failure = degradeFailureIfAbortFailed(ex.ctx, ex.requestID, classifiedFailureFromDecision(clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, err), abortErr)
		return outcome
	}
	if dispatchRes == nil || dispatchRes.UpstreamReader == nil {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_empty_response", ex.requestID, 0, ex.protocolLoss)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, 0, time.Since(upstreamAttemptStartedAt), ex.requestID, nil, 0)
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
	ex.updateSessionWindowFromHeaders(dispatchRes.Headers)
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
	decision.ClientStatus = ex.remapClientStatusForUpstream(dispatchRes.StatusCode, decision.ClientStatus)
	recordModelCooldownOnUpstream404(ex.ctx, ex.d, ex.ident.TenantID, ex.acquiredAccountID, ex.upstreamModelID, dispatchRes.StatusCode, ex.requestID)
	abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0, ex.protocolLoss)
	if ex.healthKeyOK {
		recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, gateway.SignalFromClassification(dispatchRes.StatusCode, classification), dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, rateLimitResetFromClassification(classification, time.Now()), gateway.AuthFailureClassFromClassification(classification))
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
	if ex.activeForceFormat() {
		streamForwarder.ForceOpenAIChatFormat = true
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
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, class, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil, 0)
		}
	} else if ex.healthKeyOK {
		recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalSuccess, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil, 0)
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
	// 20 计费策略,且避免 abort 已交付内容的流导致重试重复交付。
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
	// 上游族 wire 形态与客户端协议同形时(kimi/qwen/cohere/... == openai_chat;
	// openai_codex 刻意不在映射表内,见 hcsfProviderRequestModelFamily 排除注释,
	// 故 responses→codex 不走此 fast-path 而 fail-closed)走 raw 直通:保真 vendor 专有字段
	// (top_k 等;流式无 mergeHCSFRawPassthroughFields,翻译会静默丢),与
	// openai→openai 既有直通语义一致。此前这些族在此返回 true 后
	// MarshalToProviderRequest 不认原始族名,所有兼容族流式请求 501 在投递前
	// 就挂(renew-156 族集不对称第 5 处变体)。
	if gateway.HCSFEndpointModelFamily(fam) == cp {
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
	seed := requestMetaSeed(ex.r, ex.ident, ex.clientProtocol, ex.resolved.ProtocolFamily, ex.routeID, ex.requestID, ex.req.Model, ex.acquiredAccountID, ex.acquisitionToken)
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
	if ex.activeForceFormat() && ex.clientProtocol == proto.ClientProtocolOpenAIChat {
		return &proto.OpenAIChatClient{ForceFormat: true}, nil
	}
	return clientAdapter, nil
}

func streamingProviderRequestBody(env *proto.HCSF, family string) ([]byte, error) {
	// 先归一到 marshal 形态族(kimi/qwen/... → openai_chat;openai_codex →
	// openai_responses;gemini_advanced_session → gemini_messages)。controls
	// 注入的字段形态(max_tokens vs max_output_tokens、gemini generationConfig)
	// 与 stream 字段注入方式都跟"形态"走;跟原始族名走会在跨协议流式
	// (anthropic→codex / openai→gemini_advanced 等)把 openai_chat 形态的
	// controls 注进 Responses/Gemini body。同形态直通路径不经过本函数
	// (needsStreamingHCSFTranslation fast-path),此处只服务真翻译路径。
	family = gateway.HCSFEndpointModelFamily(family)
	body, err := gateway.MarshalToProviderRequest(env, family)
	if err != nil {
		return nil, err
	}
	body, err = injectStreamingRequestControls(body, env, family)
	if err != nil {
		return nil, err
	}
	switch family {
	case "gemini_messages", "dify_chat", "ollama_native":
		// 这三族跳过 forceStreamingRequest:gemini 的流式开关由 endpoint 路径
		// (streamGenerateContent)决定,dify 由 body 内 response_mode 决定,
		// 两者注 openai 形 stream:true 即污染 body;ollama_native 的 stream
		// 字段已由 marshal 按 StreamPlan 显式写入(再注 true 虽幂等,但 stream
		// 字段的真相源必须唯一收敛在 marshal,禁止两处写)。
		return body, nil
	}
	return forceStreamingRequest(body)
}

func injectStreamingRequestControls(raw []byte, env *proto.HCSF, family string) ([]byte, error) {
	if family == "dify_chat" {
		// Dify 无 per-request 控制参数(模型/采样在 app 侧配置),openai 形
		// controls 字段一律不可注入;被丢弃的 controls 已在 marshal 内记 loss。
		return raw, nil
	}
	if family == "ollama_native" {
		// Ollama 的采样控制已在 marshal 阶段嵌进 options{}(num_predict 等);
		// 顶层二次注入 openai 形 max_tokens/temperature 是协议污染。
		return raw, nil
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if family == "gemini_messages" {
		return injectStreamingGeminiRequestControls(body, env)
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
	mergeStreamingRequestPassthrough(body, env)
	return json.Marshal(body)
}

func mergeStreamingRequestPassthrough(body map[string]any, env *proto.HCSF) {
	if env == nil || env.Passthrough == nil || len(env.Passthrough.Extra) == 0 {
		return
	}
	for key, raw := range env.Passthrough.Extra {
		if _, exists := body[key]; exists {
			continue
		}
		body[key] = streamingRawJSONValue(raw)
	}
}
func injectStreamingGeminiRequestControls(body map[string]any, env *proto.HCSF) ([]byte, error) {
	c := env.RequestControls
	generation := map[string]any{}
	if existing, ok := body["generationConfig"].(map[string]any); ok {
		for k, v := range existing {
			generation[k] = v
		}
	}
	if c.MaxTokens != nil {
		generation["maxOutputTokens"] = *c.MaxTokens
	}
	if c.Temperature != nil {
		generation["temperature"] = *c.Temperature
	}
	if c.TopP != nil {
		generation["topP"] = *c.TopP
	}
	if len(c.StopSequences) > 0 {
		generation["stopSequences"] = c.StopSequences
	} else if len(c.Stop) > 0 {
		generation["stopSequences"] = c.Stop
	}
	if c.ResponseFormat != nil && len(c.ResponseFormat.Schema) > 0 {
		var raw map[string]any
		if json.Unmarshal(c.ResponseFormat.Schema, &raw) == nil {
			if v, ok := raw["responseMimeType"]; ok && v != "" {
				generation["responseMimeType"] = v
			}
			if v, ok := raw["responseSchema"]; ok {
				generation["responseSchema"] = v
			}
		}
	}
	if len(generation) > 0 {
		body["generationConfig"] = generation
	}
	if len(c.Tools) > 0 {
		body["tools"] = streamingGeminiControlTools(c.Tools)
	}
	return json.Marshal(body)
}

func streamingGeminiControlTools(tools []proto.CanonicalTool) []any {
	decls := make([]any, 0, len(tools))
	for _, tool := range tools {
		decls = append(decls, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  streamingRawJSONValue(tool.InputSchema),
		})
	}
	if len(decls) == 0 {
		return nil
	}
	return []any{map[string]any{"functionDeclarations": decls}}
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

// ambiguousDeliveredEstimable 判断歧义用量流是否已交付可估内容：可估输出基数
// （可见输出 + 可见 reasoning 文本）>0 即说明确有内容发给了用户。仅此种歧义流放行
// 估算保守计费——reconciliation 是 refund-only/zero-finalize 永不补收，留歧义态会
// 永久漏收（SM-05）；无可估内容的歧义流仍保留歧义态留待真对账。判据与 billing/
// state.go AttemptFromGatewayDraft 的 Ambiguous 分支保持同口径。
func ambiguousDeliveredEstimable(draft gateway.UsageRecordDraft) bool {
	return draft.EstimatedOutputTokens+draft.EstimatedReasoningTokens > 0
}

func (ex *chatExecution) streamingCompletionEvent(draft gateway.UsageRecordDraft, streamAttempt billing.Attempt, ledgerResult auditledger.AuditLedgerResult) eventbus.RequestCompletionEvent {
	usage := usageFromDraft(draft)
	usageBasisEstimated := false
	actualCost, err := ex.actualCompletionCost(usage)
	if err != nil {
		draft.PendingReconciliation = true
		actualCost = completionCostBreakdown{}
		// 缺上游 usage（无任何 token 信号）但已交付内容：不把 DeliveredTokenCount 当 token 计费——
		// 它此处是内容帧数（canonicalDeliveredChunks）而非 token 数；细碎 tool_input/sub-token 分帧
		// 会使帧数 > 真实 token 数，按帧计费会向用户多收。
		// 仅在确为缺 usage 时走估算/inferred：计费配置失败（rate table 缺失但有真实 token）不可标 inferred，
		// 否则 worker 会把真实请求零差额定稿成 $0（静默零计费）。
		// 歧义用量（unknown termination 等）默认保留歧义态留待真对账，不降级成 inferred、
		// 不被估算终局计费——除非已向用户交付可估内容（EstimatedOutputTokens+
		// EstimatedReasoningTokens>0）：此时内容已发出，而 reconciliation 是 refund-only/
		// zero-finalize 永不补收，留歧义态 = 永久零收漏钱（SM-05）。故对「歧义且有可估交付」
		// 放行估算保守计费，与 billing/state.go AttemptFromGatewayDraft 同口径判据一致；
		// 无可估交付的歧义流仍跳过，保留歧义态。
		if reportedUsageMissing(usage) &&
			(draft.UsageSource != gateway.UsageSourceAmbiguous || ambiguousDeliveredEstimable(draft)) {
			// 估算兜底：终帧缺失/无 usage 帧的流（部分 serving 上游不保证 usage）按
			// 逐事件可见内容估算终局计费，token 基数写回 draft 留账，inferred +
			// usage_basis 快照标记构成审计链；不挂 pending（no-usage 定稿 SQL 只认
			// 全零记录，挂上即永久 pending）。估算不可用（零可见内容/费率表故障）→
			// 维持 ActualCost=0 + pending + inferred，交由 settlementreconcile worker
			// 宽限后零差额定稿（无权威 usage 会到达）。
			if cost, estimated, ok := ex.estimatedStreamingCost(draft); ok {
				actualCost = cost
				draft.TokensInput = estimated.InputTokens
				draft.TokensOutput = estimated.OutputTokens
				draft.UsageSource = gateway.UsageSourceInferred
				draft.PendingReconciliation = false
				usageBasisEstimated = true
			} else if draft.DeliveredTokenCount > 0 {
				draft.UsageSource = gateway.UsageSourceInferred
			}
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
	// pending 与上方缺 usage 的 pending 取并集,不互相覆盖。
	streamConfidence, streamPending := crossCheckAudit(draft.TokensOutput, draft.ReasoningTokens, draft.EstimatedOutputTokens, draft.EstimatedReasoningTokens, actualCost.Total.IsPositive())
	if usageBasisEstimated {
		// 估算计费行:交叉校验是估算值自比对,恒满置信且可能在畸形 usage(只报
		// reasoning)下误挂 pending——估算行的 pending 无人能定稿(no-usage 定稿
		// SQL 只认全零)。改记固定降级置信,pending 强制清零,保持终局语义。
		streamConfidence, streamPending = estimatedUsageBasisConfidence, false
	}
	draft.ConfidenceScore = &streamConfidence
	if streamPending || actualCost.PendingReconciliation {
		draft.PendingReconciliation = true
	}
	draft = withOriginAudit(draft, ex.r, ex.d)
	draft.ClientTool = clientToolFromContext(ex.ctx)
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
			// 后者之前被 StreamForwarder 丢弃,只有初始(常为空)请求侧损失能到 settle(item 4)。
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
