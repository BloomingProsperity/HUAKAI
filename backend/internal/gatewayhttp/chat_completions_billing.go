package gatewayhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
)

// Above the heuristic estimator's own noise floor; sub-floor deltas are false-positive-dominated.
const crossCheckMinAbsTokenDelta = 50

// crossCheckAudit 计算 token 交叉校验的审计信号(confidence_score / pending_reconciliation)。
// reportedOutput = 上游报告 OutputTokens;reasoningTokens = 其中隐藏推理 token(o1/o3,
// 客户端不可见、估算器数不到),先扣除得可见输出再与 estimated(可见内容估算)比对。
// estimatedReasoning = 流式累加的可见 reasoning 文本估算(非流路径传 0,其估算器已含思考块)。
// 仅在 hasPositiveCost(排除 cache-hit 零成本回放)且绝对偏差 >= 下限时才降级,避免短响应/
// 估算噪声误报;estimated<=0(无可估内容)→ CrossCheck 返回 Unknown → 不降级。
// 非流(nonStreamingUsageDraft)与流式(streamingCompletionEvent)共用,确保口径一致
func crossCheckAudit(reportedOutput, reasoningTokens, estimated, estimatedReasoning int, hasPositiveCost bool) (confidence float64, pending bool) {
	confidence = 1.0
	// reasoning 文本被流出(estimatedReasoning>0)却无对应 ReasoningTokens 单列时,无法判断
	// reported OutputTokens 是否已含 thinking:Anthropic 扩展思考把 thinking 计入 output_tokens,
	// 而 Gemini thought 不计入 candidatesTokenCount,canonical 层无此 folding 信号。任一方向的
	// 加/减都会误判一类 provider,故此处跳过交叉校验(保持满置信、不 pending),宁可在 thinking 流上
	// 少覆盖也不误报主路径 Claude thinking 流量(reasoning-aware 校验见延后 spec)。
	if reasoningTokens == 0 && estimatedReasoning > 0 {
		return confidence, pending
	}
	visibleOutput := reportedOutput - reasoningTokens
	if visibleOutput < 0 {
		visibleOutput = 0
	}
	verdict := tokencheck.CrossCheck(visibleOutput, estimated).Verdict
	delta := visibleOutput - estimated
	if delta < 0 {
		delta = -delta
	}
	if hasPositiveCost && delta >= crossCheckMinAbsTokenDelta {
		switch verdict {
		case tokencheck.VerdictWarn5:
			confidence = 0.8
		case tokencheck.VerdictFail20:
			confidence = 0.5
			pending = true
		}
	}
	return confidence, pending
}

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
	ledgerResult, err := submitAuditLedgerEntry(ex.ctx, ex.d, bufferedEnv, ex.ident.TenantID, ex.requestID)
	// 快照取在 submitAuditLedgerEntry 之后:它会向 env.CapabilityGraph.ProtocolLoss
	// 追加 ledger/trust-chain 警告(appendTrustChainWarning),提前快照会漏掉这些证据,
	// 使 audit_ledger_error abort 与成功 settle 的 protocol_loss 缺 ledger 侧损失(item 2)。
	ex.protocolLoss = protocolLossJSONFromEnv(bufferedEnv)
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "audit_ledger_error", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusInternalServerError, clienterr.CodeAuditLedgerError, err)
		return markAttemptOutcomeDelivered(outcome)
	}
	seed := requestMetaSeed(ex.r, ex.ident, ex.clientProtocol, ex.resolved.ProtocolFamily, ex.routeID, ex.requestID, ex.req.Model, ex.acquiredAccountID, ex.acquisitionToken)
	seed.ForceFormat = ex.activeForceFormat()
	seedCtx := proto.ContextWithRequestMetaSeed(ex.ctx, seed)
	clientBody, clientLosses, err := ex.clientAdapter.CanonicalToClientResponse(seedCtx, bufferedEnv)
	// 响应转换损失之前被丢弃(_);折入 env 并重新快照,使成功 settle 与
	// canonical_response_error abort 都带上响应侧证据(item 2)。
	if len(clientLosses) > 0 {
		bufferedEnv.CapabilityGraph.ProtocolLoss = append(bufferedEnv.CapabilityGraph.ProtocolLoss, clientLosses...)
		ex.protocolLoss = protocolLossJSONFromEnv(bufferedEnv)
	}
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "canonical_response_error", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, err)
		return markAttemptOutcomeDelivered(outcome)
	}
	cacheEnvelope, cacheEnvelopeOK := encodeL2CacheEnvelope(bufferedEnv)
	actualCost, err := ex.actualCompletionCost(usageFromBufferedEnvelope(bufferedEnv))
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "pricing_unavailable", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, err)
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
		AuditLedgerID:             ledgerID(ledgerResult),
		AuditLedgerDLQRef:         ledgerDLQRef(ledgerResult),
		AuditSignatureFingerprint: ledgerFingerprint(ledgerResult),
		SettleRequest:             settleReq,
		Metadata:                  completionMetadata(ex.routeID, ex.clientRequestID),
	}); err != nil {
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusInternalServerError, settleErrorCode(err), err)
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
	WriteHuakaiHeaders(w.Header(), ex.req.Model, bufferedEnv, ledgerResult, ex.requestID, ex.ident.TenantID, ex.d.Signer)
	outcome = ex.baseAttemptOutcome()
	outcome.Success = &attemptSuccess{
		StatusCode: http.StatusOK,
		Body:       clientBody,
	}
	return outcome
}

// clientToolFromContext 取 clientid 中间件归一出的非敏感客户端工具枚举(空=未知)。
// W4:供 chat 各 settle 路径(非流/流式/cache-hit)统一接客户端归因。
func clientToolFromContext(ctx context.Context) string {
	return clientid.ToolFromContext(ctx)
}

func (ex *chatExecution) nonStreamingSettleRequest(env *proto.HCSF, actualCost completionCostBreakdown, routingReason []byte) billing.SettleRequest {
	draft := withOriginAudit(nonStreamingUsageDraft(env, actualCost, routingReason), ex.r, ex.d)
	draft.ClientTool = clientToolFromContext(ex.ctx)
	return billing.SettleRequest{
		ClaimID:             ex.reserveRes.ClaimID,
		AccountID:           ex.acquiredAccountID,
		AcquisitionToken:    ex.acquisitionToken,
		TenantID:            ex.ident.TenantID,
		APIKeyID:            ex.ident.APIKeyID,
		UserID:              ex.ident.UserID,
		ProviderAccountID:   ex.acquiredAccountID,
		AttemptSeq:          int32(ex.activeAttemptSeq()),
		RequestedModel:      ex.req.Model,
		UpstreamModel:       ex.upstreamModelID,
		Provider:            ex.cacheVendor,
		Stream:              false,
		ActualCost:          actualCost.Total,
		ProtocolLoss:        ex.protocolLoss,
		Fingerprint:         ex.payloadHash,
		Draft:               draft,
		EmitSchedulerOutbox: true,
		SnapshotVersion:     ex.plan.SnapshotVersion,
	}
}

func withOriginAudit(draft gateway.UsageRecordDraft, r *http.Request, d ChatHandlerDeps) gateway.UsageRecordDraft {
	if r == nil {
		return draft
	}
	draft.IPAddress = boundedAuditStringPtr(d.ClientIPResolver.ClientIP(r), 128)
	draft.UserAgent = boundedAuditStringPtr(r.Header.Get("User-Agent"), 512)
	return draft
}

func boundedAuditStringPtr(raw string, maxRunes int) *string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if maxRunes > 0 {
		n := 0
		for i := range s {
			if n == maxRunes {
				s = s[:i]
				break
			}
			n++
		}
	}
	return &s
}

func protocolLossJSONFromEnv(env *proto.HCSF) json.RawMessage {
	if env == nil {
		return nil
	}
	return protocolLossJSONFromEntries(env.CapabilityGraph.ProtocolLoss)
}

func protocolLossJSONFromEntries(entries []proto.ProtocolLossEntry) json.RawMessage {
	if len(entries) == 0 {
		return nil
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return nil
	}
	return raw
}

// mergeProtocolLossWithEntries 合并已序列化的 base(请求翻译损失)与追加的 typed
// 条目(流式逐事件 provider/client 损失),一次性 marshal(item 4)。
// 顺序:base 在前、entries 在后(请求 → 逐事件发射序);不去重(每条独立观测,审计全留)。
// base 不可解析时退化为仅返回 base,绝不静默丢已有请求侧证据。
func mergeProtocolLossWithEntries(base json.RawMessage, entries []proto.ProtocolLossEntry) json.RawMessage {
	if len(entries) == 0 {
		return base
	}
	var baseEntries []proto.ProtocolLossEntry
	if len(base) > 0 {
		if err := json.Unmarshal(base, &baseEntries); err != nil {
			return base
		}
	}
	merged := append(baseEntries, entries...)
	return protocolLossJSONFromEntries(merged)
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

// settleCompletionWithRecovery 包 settleCompletion,在 post-delivery 场景
// (响应已发给客户端 + settle 失败)把 RequestCompletionEvent 转
// settlementrecovery.Payload enqueue 进 usage_record_dlq,worker 后续重放
// Settler.Settle。
//
// 调用约定:
//   - source != "" 表示 "已交付内容 给客户端" — settle 失败必须 durable 兜底
//   - source == "" 或 SettleRecoveryDLQ == nil — 跟原 settleCompletion 一致,
//     失败只返 err,caller 自决(stream/billing pre-delivery path 返 5xx 给客户端)
//
// Enqueue 自己失败时 P0 log alert(Owner D-4 已批 — 不再 disk spool,只 alert),
// 但不阻塞:流式响应已发给客户端不能反悔。
//
// settle err 始终原样传给 caller,跟 settleCompletion 行为一致。
func settleCompletionWithRecovery(ctx context.Context, d ChatHandlerDeps, event eventbus.RequestCompletionEvent, source settlementrecovery.Source) (*billing.SettleResult, error) {
	res, err := settleCompletion(ctx, d, event)
	if err == nil {
		return res, nil
	}
	if source == "" || d.SettleRecoveryDLQ == nil {
		return res, err
	}
	payload := settlementrecovery.FromCompletionEvent(source, event)
	// post-delivery recovery DLQ 是持久运维元数据,也只能保留错误类别,不能保留 settle 原始错误文本。
	settleFailureClass := privacy.ErrorClassFor(ctx, err)
	if _, enqErr := settlementrecovery.EnqueuePayload(ctx, d.SettleRecoveryDLQ, payload, settleFailureClass); enqErr != nil {
		// DLQ persist 自己失败 = money path 双环灰区 (Owner D-4: 只 alert,不 disk spool)。
		_ = privacy.LogSystem(ctx, privacy.SystemEvent{
			Severity:   privacy.SeverityError,
			Component:  "gatewayhttp.settle_recovery",
			RequestID:  event.RequestID,
			ErrorClass: privacy.ErrorClassFor(ctx, enqErr),
			Attrs: map[string]any{
				"event_class":          "settle_recovery_dlq_enqueue_failed",
				"event_type":           string(source),
				"tenant_id":            event.TenantID,
				"claim_id":             event.ClaimID,
				"failure_reason_class": settleFailureClass,
			},
		})
	}
	return res, err
}

func settleCompletion(ctx context.Context, d ChatHandlerDeps, event eventbus.RequestCompletionEvent) (*billing.SettleResult, error) {
	if event.SettleRequest.AuditRequestID == "" {
		event.SettleRequest.AuditRequestID = event.RequestID
	}
	if d.CompletionBus == nil {
		if err := validateMoneyPathAuditRefForSource(ctx, d, event, "direct_settle"); err != nil {
			return nil, rejectMoneyPathDirectSettle(ctx, d, event, err)
		}
		return d.Settler.Settle(ctx, event.SettleRequest)
	}
	if err := d.CompletionBus.Emit(ctx, event); err != nil {
		if shouldDirectSettleFallback(err) {
			if err := validateMoneyPathAuditRefForSource(ctx, d, event, "direct_settle"); err != nil {
				return nil, rejectMoneyPathDirectSettle(ctx, d, event, err)
			}
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

func validateMoneyPathAuditRefForSource(ctx context.Context, d ChatHandlerDeps, event eventbus.RequestCompletionEvent, source string) error {
	if err := eventbus.ValidateMoneyPathAuditRef(&event, d.AuditRefPolicy); err != nil {
		return err
	}
	if missingMoneyPathAuditRef(event) && moneyPathAuditRefEscapeActive(d.AuditRefPolicy) {
		logMoneyPathAuditRefError(ctx, event, eventbus.ErrAuditRefMissing, source, true)
	}
	return nil
}

func rejectMoneyPathDirectSettle(ctx context.Context, d ChatHandlerDeps, event eventbus.RequestCompletionEvent, validationErr error) error {
	err, _ := rejectMoneyPathAuditRef(ctx, d, event, validationErr, "direct_settle")
	return err
}

func rejectMoneyPathCacheHitCommit(ctx context.Context, d ChatHandlerDeps, event eventbus.RequestCompletionEvent, validationErr error) (error, error) {
	return rejectMoneyPathAuditRef(ctx, d, event, validationErr, "cache_hit_commit")
}

func rejectMoneyPathAuditRef(ctx context.Context, d ChatHandlerDeps, event eventbus.RequestCompletionEvent, validationErr error, source string) (error, error) {
	if validationErr == nil {
		validationErr = eventbus.ErrAuditRefMissing
	}
	var abortErr error
	if d.Settler != nil && event.ClaimID > 0 {
		// 复用事件已携带的 protocol_loss 证据(event.SettleRequest.ProtocolLoss),
		// 这条 audit-ref-missing 的零成本 abort 是该 claim 唯一持久行;之前传 nil
		// 会丢失损失证据(item 3)。不新增 eventbus 字段。
		abortErr = d.Settler.Abort(ctx, event.TenantID, event.ClaimID, clienterr.CodeAuditRefMissing, event.RequestID, 0, event.SettleRequest.ProtocolLoss)
	}
	logMoneyPathAuditRefError(ctx, event, validationErr, source, false)
	if abortErr != nil {
		return fmt.Errorf("settle rejected: %w; abort %s: %v", validationErr, clienterr.CodeAuditRefMissing, abortErr), abortErr
	}
	return fmt.Errorf("settle rejected: %w", validationErr), nil
}

func logMoneyPathAuditRefError(ctx context.Context, event eventbus.RequestCompletionEvent, validationErr error, source string, escapeFlagActive bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestID := event.RequestID
	if requestID == "" {
		requestID = event.ID
	}
	if validationErr == nil {
		validationErr = eventbus.ErrAuditRefMissing
	}
	state := "enforced"
	if escapeFlagActive {
		state = "escape_flag_active"
	}
	_ = privacy.LogSystem(ctx, privacy.SystemEvent{
		Severity:   privacy.SeverityError,
		Component:  "gatewayhttp.money_path",
		RequestID:  requestID,
		ErrorClass: privacy.ErrorClassFor(ctx, validationErr),
		Attrs: map[string]any{
			"event_class":  "money_path_audit_ref_missing",
			"event_type":   source,
			"tenant_id":    event.TenantID,
			"route_id":     routeIDFromEvent(event),
			"claim_id":     event.ClaimID,
			"reason_class": clienterr.CodeAuditRefMissing,
			"state":        state,
		},
	})
}

func missingMoneyPathAuditRef(event eventbus.RequestCompletionEvent) bool {
	return !(event.AuditLedgerDLQRef != "" || (event.AuditLedgerID != "" && event.AuditSignatureFingerprint != ""))
}

func moneyPathAuditRefEscapeActive(policy *eventbus.AuditRefPolicy) bool {
	return policy != nil &&
		policy.ReleaseMode == eventbus.ReleaseModeProduction &&
		policy.AllowMissingMoneyRef
}

func routeIDFromEvent(event eventbus.RequestCompletionEvent) string {
	if event.Metadata == nil {
		return ""
	}
	return event.Metadata["route_id"]
}

func routeMetadata(routeID string) map[string]string {
	if routeID == "" {
		return nil
	}
	return map[string]string{"route_id": routeID}
}

func completionMetadata(routeID, clientRequestID string) map[string]string {
	metadata := routeMetadata(routeID)
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" {
		return metadata
	}
	if metadata == nil {
		metadata = make(map[string]string, 1)
	}
	metadata["client_request_id"] = clientRequestID
	return metadata
}

func settleErrorCode(err error) string {
	if errors.Is(err, eventbus.ErrAuditRefMissing) {
		return clienterr.CodeAuditRefMissing
	}
	return clienterr.CodeSettleError
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

func ledgerID(result auditledger.AuditLedgerResult) string {
	if result.State != auditledger.LedgerResultStatePersisted {
		return ""
	}
	return result.LedgerID
}

func ledgerFingerprint(result auditledger.AuditLedgerResult) string {
	if result.State != auditledger.LedgerResultStatePersisted {
		return ""
	}
	return result.Fingerprint
}

func ledgerDLQRef(result auditledger.AuditLedgerResult) string {
	if result.State != auditledger.LedgerResultStateDeferred {
		return ""
	}
	return result.DLQRef
}

func requestMetaSeed(r *http.Request, ident auth.Identity, clientProtocol proto.ClientProtocol, protocolFamily, routeID, requestID, model string, accountID int64, acquisitionToken uuid.UUID) proto.RequestMetaSeed {
	token := ""
	if acquisitionToken != uuid.Nil {
		token = acquisitionToken.String()
	}
	return proto.RequestMetaSeed{
		RequestID:        requestID,
		ClientProtocol:   clientProtocol,
		ProtocolFamily:   protocolFamily,
		IngressPath:      r.URL.Path,
		Model:            model,
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
	if sessionHash != "" {
		env.RequestMeta.SessionHash = sessionHash
	}
	if env.RequestMeta.EvidenceLabel == "" {
		env.RequestMeta.EvidenceLabel = proto.EvidenceMock
	}
	if env.Accounting.EvidenceLabel == "" {
		env.Accounting.EvidenceLabel = proto.EvidenceMock
	}
}

func submitAuditLedgerEntry(ctx context.Context, d ChatHandlerDeps, env *proto.HCSF, tenantID int64, requestID string) (auditledger.AuditLedgerResult, error) {
	production := auditLedgerProductionMode()
	if env == nil {
		return auditledger.DisabledLedgerResult(), nil
	}
	if d.AuditLedger == nil {
		appendTrustChainWarning(env, "audit_ledger_not_configured", "audit ledger dependency unset; trust-chain ledger entry skipped")
		if production {
			return auditledger.AuditLedgerResult{}, fmt.Errorf("audit ledger dependency unset in production")
		}
		return auditledger.DisabledLedgerResult(), nil
	}
	if auditledger.IsNoopLedger(d.AuditLedger) {
		appendTrustChainWarning(env, "audit_ledger_noop", "audit ledger dependency is noop; trust-chain ledger entry skipped")
		if production {
			return auditledger.AuditLedgerResult{}, fmt.Errorf("audit ledger noop in production")
		}
		return auditledger.DisabledLedgerResult(), nil
	}
	if requestID == "" {
		requestID = env.RequestMeta.RequestID
	}
	entry := auditledger.LedgerEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		RequestID:  requestID,
		TenantID:   tenantID,
		HopChain:   cloneHopChain(env.Accounting.HopChain),
		ModelChain: cloneModelChain(env.Accounting.ModelChain),
	}
	if d.Signer == nil {
		appendTrustChainWarning(env, "audit_signer_not_configured", "audit signer dependency unset; trust-chain ledger entry skipped (fail-open D-8)")
		mode := "dev"
		if production {
			mode = "production"
		}
		log.Printf("[trust-chain] WARN: signer unavailable in %s mode, fail-open with unverified status (request_id=%s)", mode, env.RequestMeta.RequestID)
		prepared, prepareErr := auditledger.PrepareEntry(ctx, entry)
		if prepareErr != nil {
			return auditledger.DisabledLedgerResult(), nil
		}
		dlqRef, dlqErr := auditledger.EnqueuePreparedEntryToDLQ(ctx, d.AuditLedgerDLQ, prepared, fmt.Errorf("audit signer dependency unset"))
		if dlqErr != nil {
			appendTrustChainWarning(env, "audit_signer_dlq_enqueue_failed", dlqErr.Error())
			return auditledger.DisabledLedgerResult(), nil
		}
		appendTrustChainWarning(env, "audit_signer_deferred", "audit signer unset; sanitized append intent queued in DLQ")
		return auditledger.DeferredLedgerResult(dlqRef), nil
	}
	prepared, err := auditledger.PrepareEntry(ctx, entry)
	if err != nil {
		return auditledger.AuditLedgerResult{}, fmt.Errorf("audit ledger prepare: %w", err)
	}
	appended, err := d.AuditLedger.Append(ctx, prepared)
	if err != nil {
		if errors.Is(err, auditledger.ErrDuplicateRequestID) {
			existing, lookupErr := d.AuditLedger.GetByRequestID(ctx, requestID)
			if lookupErr != nil {
				return auditledger.AuditLedgerResult{}, fmt.Errorf("audit ledger duplicate lookup: %w", lookupErr)
			}
			if existing.TenantID != tenantID {
				return auditledger.AuditLedgerResult{}, fmt.Errorf("audit ledger duplicate tenant mismatch: request_id=%q tenant_id=%d existing_tenant_id=%d", requestID, tenantID, existing.TenantID)
			}
			env.Accounting.LedgerID = existing.LedgerID
			env.Accounting.Signature = existing.Signature
			env.Accounting.PubkeyFingerprint = existing.PubkeyFingerprint
			result := auditledger.PersistedLedgerResult(existing)
			if err := result.Validate(production); err != nil {
				return auditledger.AuditLedgerResult{}, err
			}
			appendTrustChainWarning(env, "audit_ledger_duplicate_request_id", "audit ledger entry already exists; reused persisted audit reference")
			return result, nil
		}
		appendTrustChainWarning(env, "audit_ledger_append_failed", err.Error())
		dlqRef, dlqErr := auditledger.EnqueuePreparedEntryToDLQ(ctx, d.AuditLedgerDLQ, prepared, err)
		if dlqErr != nil {
			appendTrustChainWarning(env, "audit_ledger_dlq_enqueue_failed", dlqErr.Error())
			return auditledger.AuditLedgerResult{}, fmt.Errorf("audit ledger append: %w; audit ledger dlq enqueue: %v", err, dlqErr)
		}
		appendTrustChainWarning(env, "audit_ledger_deferred", "audit ledger append failed; sanitized append intent queued in DLQ")
		result := auditledger.DeferredLedgerResult(dlqRef)
		if err := result.Validate(production); err != nil {
			return auditledger.AuditLedgerResult{}, err
		}
		return result, nil
	}
	env.Accounting.LedgerID = appended.LedgerID
	env.Accounting.Signature = appended.Signature
	env.Accounting.PubkeyFingerprint = appended.PubkeyFingerprint
	result := auditledger.PersistedLedgerResult(appended)
	if err := result.Validate(production); err != nil {
		return auditledger.AuditLedgerResult{}, err
	}
	return result, nil
}

func auditLedgerProductionMode() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_RELEASE_MODE")), "production")
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

func nonStreamingUsageDraft(env *proto.HCSF, actualCost completionCostBreakdown, routingReason []byte) gateway.UsageRecordDraft {
	usage := proto.CanonicalUsage{}
	if env != nil {
		usage = env.Accounting.Usage
		if env.BufferedResponse != nil {
			usage = env.BufferedResponse.Usage
		}
	}
	cacheCreationTokens := usage.CacheCreationInputTokens
	if cacheCreationTokens == 0 {
		cacheCreationTokens = usage.CacheCreationInputTokens5m + usage.CacheCreationInputTokens1h
	}
	estimatedOutputTokens := 0
	if env != nil && env.BufferedResponse != nil {
		estimatedOutputTokens = tokencheck.HeuristicEstimator{}.Estimate(env.BufferedResponse.Content)
	}
	// 非流估算器(HeuristicEstimator.Estimate)对 buffered thinking 块已计入估算(estimateBlock
	// 计 block.Thinking),与 reported 口径自洽,故 estimatedReasoning 传 0、不触发 folding-跳过。
	confidence, pendingReconciliation := crossCheckAudit(usage.OutputTokens, usage.ReasoningTokens, estimatedOutputTokens, 0, actualCost.Total.IsPositive())
	return gateway.UsageRecordDraft{
		TokensInput:           usage.InputTokens,
		TokensOutput:          usage.OutputTokens,
		DeliveredTokenCount:   int64(usage.OutputTokens),
		CacheCreationTokens:   cacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreationInputTokens5m,
		CacheCreation1hTokens: usage.CacheCreationInputTokens1h,
		CacheReadTokens:       usage.CacheReadInputTokens,
		ActualCost:            actualCost.Total,
		CostSnapshot:          actualCost.CostSnapshot,
		CacheCreationCost:     actualCost.CacheCreationCost,
		CacheReadCost:         actualCost.CacheReadCost,
		RoutingReason:         routingReason,
		EndClass:              gateway.StreamEndGraceful,
		UsageSource:           gateway.UsageSourceReported,
		ConfidenceScore:       &confidence,
		DrainOutcome:          gateway.DrainNotDrained,
		PendingReconciliation: pendingReconciliation || actualCost.PendingReconciliation,
	}
}
