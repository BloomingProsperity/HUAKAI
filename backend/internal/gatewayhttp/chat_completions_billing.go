package gatewayhttp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func (ex *chatExecution) handleNonStreamingResponse(w http.ResponseWriter) {
	outcome := ex.executeNonStreamingAttempt(w)
	if outcome.Success != nil {
		writeAttemptSuccess(w, outcome)
	}
}

func (ex *chatExecution) executeNonStreamingAttempt(w http.ResponseWriter) attemptOutcome {
	outcome := ex.baseAttemptOutcome()
	bufferedEnv, failure, ok := ex.dispatchBufferedEnvelope(w)
	if !ok {
		if failure != nil {
			outcome = ex.baseAttemptOutcome()
			outcome.Failure = failure
			return outcome
		}
		return markAttemptOutcomeDelivered(outcome)
	}
	ledgerEntry, err := submitAuditLedgerEntry(ex.ctx, ex.d, bufferedEnv, ex.ident.TenantID, ex.requestID)
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "audit_ledger_error", ex.requestID, 0); abortErr != nil {
			w.Header().Set("X-Huakai-Abort-Failed", abortErr.Error())
		}
		writeJSONError(w, http.StatusInternalServerError, "audit_ledger_error", err.Error())
		return markAttemptOutcomeDelivered(outcome)
	}
	seed := requestMetaSeed(ex.r, ex.ident, ex.clientProtocol, ex.resolved.ProtocolFamily, ex.routeID, ex.requestID, ex.acquiredAccountID, ex.acquisitionToken)
	seedCtx := proto.ContextWithRequestMetaSeed(ex.ctx, seed)
	clientBody, _, err := ex.clientAdapter.CanonicalToClientResponse(seedCtx, bufferedEnv)
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "canonical_response_error", ex.requestID, 0); abortErr != nil {
			w.Header().Set("X-Huakai-Abort-Failed", abortErr.Error())
		}
		writeJSONError(w, http.StatusBadGateway, "canonical_response_error", err.Error())
		return markAttemptOutcomeDelivered(outcome)
	}
	cacheEnvelope, cacheEnvelopeOK := encodeL2CacheEnvelope(bufferedEnv)
	actualCost, err := ex.actualCompletionCost(usageFromBufferedEnvelope(bufferedEnv))
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "pricing_unavailable", ex.requestID, 0); abortErr != nil {
			w.Header().Set("X-Huakai-Abort-Failed", abortErr.Error())
		}
		writeJSONError(w, http.StatusServiceUnavailable, "pricing_unavailable", err.Error())
		return markAttemptOutcomeDelivered(outcome)
	}
	settleReq := ex.nonStreamingSettleRequest(bufferedEnv, actualCost, ex.selRes.RoutingReasonJSON)
	if _, err := settleCompletion(ex.ctx, ex.d, eventbus.RequestCompletionEvent{
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
		AuditLedgerID:             ledgerID(ledgerEntry),
		AuditSignatureFingerprint: ledgerFingerprint(ledgerEntry),
		SettleRequest:             settleReq,
	}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "settle_error", err.Error())
		return markAttemptOutcomeDelivered(outcome)
	}
	// 持久幂等重放: 存原始响应供同 Idempotency-Key 重试路由无关地重放。
	ex.recordIdempotencyReplay(ex.reserveRes.ClaimID, http.StatusOK, clientBody)
	if ex.d.ResponseCache != nil && ex.cacheKey != "" && cacheEnvelopeOK {
		// retry/failover 可能跨 upstream model 成功；cache 写入必须使用
		// 实际成功 attempt 的 model，避免把 fallback 响应写进 primary key。
		if cacheKey, err := ex.l2CacheKeyForModel(ex.upstreamModelID); err == nil {
			ex.d.ResponseCache.Set(ex.ctx, cacheEntry(ex, cacheKey, clientBody, cacheEnvelope))
			syncL2SizeMetrics(ex.d.ResponseCache)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if ex.d.ResponseCache != nil && ex.cacheKey != "" {
		w.Header().Set("X-HUAKAI-Cache-L2", "miss")
	}
	WriteHuakaiHeaders(w.Header(), ex.req.Model, bufferedEnv, ledgerEntry)
	outcome = ex.baseAttemptOutcome()
	outcome.Success = &attemptSuccess{
		StatusCode: http.StatusOK,
		Body:       clientBody,
	}
	return outcome
}

func (ex *chatExecution) nonStreamingSettleRequest(env *proto.HCSF, actualCost decimal.Decimal, routingReason []byte) billing.SettleRequest {
	return billing.SettleRequest{
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
		Stream:            false,
		ActualCost:        actualCost,
		Fingerprint:       ex.payloadHash,
		Draft:             nonStreamingUsageDraft(env, actualCost, routingReason),
		SnapshotVersion:   ex.plan.SnapshotVersion,
	}
}

// normalizedPayloadHash 对客户端原始请求体做 SHA256 摘要, 作为 idempotency
// fingerprint。
//
// 旧实现只 hash (model, messages), 客户端可以用同 Idempotency-Key 但带不同
// input / system / tools / temperature / max_tokens 等字段重放, hash 不变
// 就被当成 replay 命中 cached claim, 出现 "同 key 不同 payload 静默复用同条
// claim → 跟实际上游响应/成本错配" 风险。
//
// 新实现 hash 原始 body 字节: 任何字段变更 (含 OpenAI /v1/responses 的 input,
// Anthropic 的 system, function calling 的 tools, 采样参数 temperature /
// top_p / max_tokens 等) 都触发新 hash, ClaimGate fingerprint conflict 检测
// 生效, 重放被拒绝。
//
// Note: 字节级 hash, 不做 JSON canonicalization。客户端 whitespace / key
// order 不同视为不同请求 — idempotency replay detection 角度合理, 因为
// 上游 upstream 实际看到的请求 body 字节才是判定 dup 的本质。
func normalizedPayloadHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func settleCompletion(ctx context.Context, d ChatHandlerDeps, event eventbus.RequestCompletionEvent) (*billing.SettleResult, error) {
	if event.SettleRequest.AuditRequestID == "" {
		event.SettleRequest.AuditRequestID = event.RequestID
	}
	if d.CompletionBus == nil {
		return d.Settler.Settle(ctx, event.SettleRequest)
	}
	if err := d.CompletionBus.Emit(ctx, event); err != nil {
		if shouldDirectSettleFallback(err) {
			return d.Settler.Settle(ctx, event.SettleRequest)
		}
		return nil, err
	}
	return &billing.SettleResult{}, nil
}

func shouldDirectSettleFallback(err error) bool {
	return errors.Is(err, eventbus.ErrNoHandlers) ||
		errors.Is(err, eventbus.ErrBusClosed) ||
		errors.Is(err, eventbus.ErrQueueFull)
}

func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func redactedBodyRef(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return "sha256:" + bodyHash(body)
}

func ledgerID(entry *auditledger.LedgerEntry) string {
	if entry == nil {
		return ""
	}
	return entry.LedgerID
}

func ledgerFingerprint(entry *auditledger.LedgerEntry) string {
	if entry == nil {
		return ""
	}
	return entry.PubkeyFingerprint
}

func requestMetaSeed(r *http.Request, ident auth.Identity, clientProtocol proto.ClientProtocol, protocolFamily, routeID, requestID string, accountID int64, acquisitionToken uuid.UUID) proto.RequestMetaSeed {
	token := ""
	if acquisitionToken != uuid.Nil {
		token = acquisitionToken.String()
	}
	return proto.RequestMetaSeed{
		RequestID:        requestID,
		ClientProtocol:   clientProtocol,
		ProtocolFamily:   protocolFamily,
		IngressPath:      r.URL.Path,
		TenantID:         ident.TenantID,
		RouteID:          routeID,
		AccountID:        accountID,
		AcquisitionToken: token,
		EvidenceLabel:    proto.EvidenceMock,
	}
}

func enrichCanonicalRequestMeta(env *proto.HCSF, upstreamModelID, providerName, idempotencyKey, sessionHash string) {
	if env == nil {
		return
	}
	env.RequestMeta.UpstreamModel = upstreamModelID
	env.RequestMeta.Provider = providerName
	env.RequestMeta.IdempotencyKey = idempotencyKey
	env.RequestMeta.SessionHash = sessionHash
	if env.RequestMeta.EvidenceLabel == "" {
		env.RequestMeta.EvidenceLabel = proto.EvidenceMock
	}
	if env.Accounting.EvidenceLabel == "" {
		env.Accounting.EvidenceLabel = proto.EvidenceMock
	}
}

func submitAuditLedgerEntry(ctx context.Context, d ChatHandlerDeps, env *proto.HCSF, tenantID int64, requestID string) (*auditledger.LedgerEntry, error) {
	if env == nil {
		return nil, nil
	}
	if d.AuditLedger == nil {
		appendTrustChainWarning(env, "audit_ledger_not_configured", "audit ledger dependency unset; trust-chain ledger entry skipped")
		return nil, nil
	}
	if d.Signer == nil {
		appendTrustChainWarning(env, "audit_signer_not_configured", "audit signer dependency unset; trust-chain ledger entry skipped")
		return nil, nil
	}
	if requestID == "" {
		requestID = env.RequestMeta.RequestID
	}
	entry := auditledger.LedgerEntry{
		LedgerID:          uuid.NewString(),
		Timestamp:         time.Now().UTC().Format(time.RFC3339Nano),
		RequestID:         requestID,
		TenantID:          tenantID,
		HopChain:          cloneHopChain(env.Accounting.HopChain),
		ModelChain:        cloneModelChain(env.Accounting.ModelChain),
		PubkeyFingerprint: d.Signer.Fingerprint(),
	}
	hash, err := auditledger.EntryHash(&entry)
	if err != nil {
		return nil, fmt.Errorf("audit ledger entry hash: %w", err)
	}
	entry.Signature = base64.StdEncoding.EncodeToString(d.Signer.Sign(hash[:]))
	appended, err := d.AuditLedger.Append(ctx, entry)
	if err != nil {
		return nil, fmt.Errorf("audit ledger append: %w", err)
	}
	env.Accounting.LedgerID = appended.LedgerID
	env.Accounting.Signature = appended.Signature
	env.Accounting.PubkeyFingerprint = appended.PubkeyFingerprint
	return &appended, nil
}

func appendTrustChainWarning(env *proto.HCSF, code, reason string) {
	if env == nil {
		return
	}
	env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, proto.ProtocolLossEntry{
		Severity: proto.ProtocolLossWarning,
		Code:     code,
		Reason:   reason,
	})
}

func fillAccountingModelUpstreamReported(env *proto.HCSF) {
	if env == nil || env.BufferedResponse == nil || env.BufferedResponse.Model == "" {
		return
	}
	mc := ensureAccountingModelChain(env)
	if mc.UpstreamReported == "" {
		mc.UpstreamReported = env.BufferedResponse.Model
	}
}

func cloneHopChain(in []proto.HopAttestation) []proto.HopAttestation {
	if in == nil {
		return nil
	}
	out := make([]proto.HopAttestation, len(in))
	copy(out, in)
	return out
}

func cloneModelChain(in *proto.ModelChain) *proto.ModelChain {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func nonStreamingUsageDraft(env *proto.HCSF, actualCost decimal.Decimal, routingReason []byte) gateway.UsageRecordDraft {
	usage := proto.CanonicalUsage{}
	if env != nil {
		usage = env.Accounting.Usage
		if env.BufferedResponse != nil {
			usage = env.BufferedResponse.Usage
		}
	}
	confidence := 1.0
	return gateway.UsageRecordDraft{
		TokensInput:           usage.InputTokens,
		TokensOutput:          usage.OutputTokens,
		DeliveredTokenCount:   int64(usage.OutputTokens),
		CacheCreationTokens:   usage.CacheCreationInputTokens,
		CacheReadTokens:       usage.CacheReadInputTokens,
		ActualCost:            actualCost,
		RoutingReason:         routingReason,
		EndClass:              gateway.StreamEndGraceful,
		UsageSource:           gateway.UsageSourceReported,
		ConfidenceScore:       &confidence,
		DrainOutcome:          gateway.DrainNotDrained,
		PendingReconciliation: false,
	}
}
