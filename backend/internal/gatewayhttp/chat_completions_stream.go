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

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
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
		if ex.reserveRes != nil {
			_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "cache_key_error", ex.requestID)
		}
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
	inboundBody := ex.body
	var clientAdapter proto.ClientAdapter
	if ex.needsStreamingHCSFTranslation() {
		var ok bool
		inboundBody, clientAdapter, ok = ex.translatedStreamingInboundBody(w)
		if !ok {
			return
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
	ex.forwardSSEAndSettle(w, dispatchRes, upstreamAttemptStartedAt, clientAdapter)
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

func (ex *chatExecution) forwardSSEAndSettle(w http.ResponseWriter, dispatchRes *gateway.DispatchResult, startedAt time.Time, clientAdapter proto.ClientAdapter) {
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
	if clientAdapter != nil {
		streamForwarder.ClientAdapter = clientAdapter
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
	event := ex.streamingCompletionEvent(draft, streamAttempt)
	if _, err := settleCompletion(ex.ctx, ex.d, event); err != nil {
		w.Header().Set("X-Huakai-Settle-Error", err.Error())
	}
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
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "streaming_adapter_unregistered", ex.requestID)
		writeJSONError(w, http.StatusServiceUnavailable, "streaming_adapter_unregistered", err.Error())
		return nil, nil, false
	}
	seed := requestMetaSeed(ex.r, ex.ident, ex.clientProtocol, ex.resolved.ProtocolFamily, ex.routeID, ex.requestID, ex.acquiredAccountID, ex.acquisitionToken)
	seedCtx := proto.ContextWithRequestMetaSeed(ex.ctx, seed)
	canonicalReq, _, err := clientAdapter.RequestToCanonical(seedCtx, ex.body)
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "invalid_request_body", ex.requestID)
		writeJSONError(w, http.StatusBadRequest, "invalid_request_body", err.Error())
		return nil, nil, false
	}
	if canonicalReq == nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "invalid_request_body", ex.requestID)
		writeJSONError(w, http.StatusBadRequest, "invalid_request_body", "client adapter returned nil canonical envelope")
		return nil, nil, false
	}
	enrichCanonicalRequestMeta(canonicalReq, ex.upstreamModelID, ex.accInfo.Platform, ex.idempotencyHeader, ex.promptHash)
	canonicalReq.RequestMeta.EndpointFamily = ex.resolved.ProtocolFamily
	setAccountingModelRequested(canonicalReq, ex.req.Model)
	setAccountingModelRouteDecided(canonicalReq, ex.forwardReq.Model)
	gateway.ApplyForwardRequestHopChain(canonicalReq, ex.forwardReq)

	body, err := streamingProviderRequestBody(canonicalReq, ex.resolved.ProtocolFamily)
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "streaming_translation_not_supported", ex.requestID)
		writeJSONError(w, http.StatusNotImplemented, "streaming_translation_not_supported", err.Error())
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

func (ex *chatExecution) streamingCompletionEvent(draft gateway.UsageRecordDraft, streamAttempt billing.Attempt) eventbus.RequestCompletionEvent {
	actualCost, err := ex.actualCompletionCost(usageFromDraft(draft))
	if err != nil {
		draft.PendingReconciliation = true
		actualCost = decimal.Zero
	}
	draft.ActualCost = actualCost
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
