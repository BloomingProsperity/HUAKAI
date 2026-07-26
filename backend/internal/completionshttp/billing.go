package completionshttp

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
)

var completionsQuotaReserveFailedOpenTotal = expvar.NewInt("completions_quota_reserve_failed_open_total")

func (ex *execution) reserve(w http.ResponseWriter) bool {
	ex.ensureIdempotency()
	predicted, err := ex.predictedCost()
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	res, err := ex.d.ClaimGate.Reserve(ex.ctx, billing.ReserveRequest{
		TenantID:                   ex.ident.TenantID,
		APIKeyID:                   ex.ident.APIKeyID,
		UserID:                     ex.ident.UserID,
		LogicalRequestID:           ex.logicalRequestID,
		EndpointFamily:             endpointFamilyCompletions,
		NormalizedPayloadHash:      ex.payloadHash,
		RequestedModel:             ex.req.Model,
		PoolingGroupID:             ex.attempt.PoolGroupID,
		BillingPolicyVersion:       ex.d.BillingPolicyVersion,
		RequestClass:               ex.d.RequestClass,
		PredictedCost:              predicted.Total,
		IdempotencyKeyClientHeader: ex.idempotencyKey,
		BalanceEnforcementMode:     ex.balanceMode(),
	})
	if errors.Is(err, billing.ErrFingerprintConflict) || (res != nil && res.FingerprintConflict) {
		writeJSONError(w, http.StatusConflict, "idempotency_conflict", "same logical_request_id with different normalized payload")
		return false
	}
	if errors.Is(err, billing.ErrInsufficientBalance) {
		writeInsufficientBalanceError(w)
		return false
	}
	if errors.Is(err, billing.ErrTenantInactive) {
		writeJSONError(w, http.StatusForbidden, clienterr.CodeTenantInactive, clienterr.MessageFor(clienterr.CodeTenantInactive))
		return false
	}
	// 幂等竞争 / Serializable 重试耗尽:可重试,返 409+Retry-After 让客户端稍后再试
	//(镜像 chat completions;此前落进下方通用 500 = 把可重试竞争误报成服务端错误)。
	if errors.Is(err, billing.ErrClaimRace) {
		w.Header().Set("Retry-After", "1")
		writeJSONError(w, http.StatusConflict, clienterr.CodeClaimRace, clienterr.MessageFor(clienterr.CodeClaimRace))
		return false
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeReserveError, clienterr.MessageFor(clienterr.CodeReserveError))
		return false
	}
	if res.IdempotencyHit {
		writeJSONError(w, http.StatusConflict, "replay_without_cache", "idempotent completions request hit but stored response unavailable; retry the request")
		return false
	}
	ex.reserveRes = res
	if err := ex.settlementIntent.InsertPending(ex.ctx, ex.ident.TenantID, ex.requestID, ex.logicalRequestID, res.ClaimID, res.AttemptSeq, ex.ident.APIKeyID, ex.payloadHash, predicted.Total); err != nil {
		_ = ex.abortWithError(w, "settlement_intent_unavailable", 0)
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeSettleError, clienterr.MessageFor(clienterr.CodeSettleError))
		return false
	}
	return ex.reserveQuota(w, predicted.Total)
}

func (ex *execution) reserveQuota(w http.ResponseWriter, predictedCost decimal.Decimal) bool {
	if ex.d.QuotaReserver == nil || ex.reserveRes == nil {
		return true
	}
	result, err := ex.d.QuotaReserver.Reserve(ex.ctx, quotaenforce.BuildReserveRequest(quotaenforce.ReserveInput{
		TenantID:           ex.ident.TenantID,
		UserID:             ex.ident.UserID,
		APIKeyID:           ex.ident.APIKeyID,
		ClaimID:            ex.reserveRes.ClaimID,
		PoolGroupID:        ex.attempt.PoolGroupID,
		RequestFingerprint: ex.payloadHash,
		RequestedModel:     ex.req.Model,
		ReservedTokens:     int64(ex.inputEstimate),
		PredictedCost:      predictedCost,
		At:                 time.Now().UTC(),
	}))
	if err == nil && result.Allowed {
		return true
	}
	if quotaenforce.IsDenied(err) || (err == nil && !result.Allowed) {
		ex.abort(w, "quota_denied", 0)
		writeInsufficientQuotaErrorRetryable(w, quotaenforce.DenyRetryAfter(result, err), quotaenforce.DenyWindowKind(result, err))
		return false
	}
	completionsQuotaReserveFailedOpenTotal.Add(1)
	slog.WarnContext(ex.ctx, "quota reserve failed open",
		slog.String("request_id", ex.requestID),
		slog.Int64("tenant_id", ex.ident.TenantID),
		slog.Int64("claim_id", ex.reserveRes.ClaimID),
		slog.String("reason", "quota_reserve_infra_error"),
		slog.String("error_type", fmt.Sprintf("%T", err)),
	)
	return true
}

func (ex *execution) settleRequest(usage completionUsage, cost completionCostBreakdown, attemptSeq int, stream bool) billing.SettleRequest {
	confidence := 1.0
	return billing.SettleRequest{
		ClaimID:               ex.reserveRes.ClaimID,
		AccountID:             ex.selRes.AccountID,
		AcquisitionToken:      ex.selRes.AcquisitionToken,
		TenantID:              ex.ident.TenantID,
		APIKeyID:              ex.ident.APIKeyID,
		UserID:                ex.ident.UserID,
		ProviderAccountID:     ex.selRes.AccountID,
		AttemptSeq:            int32(attemptSeq),
		RequestedModel:        ex.req.Model,
		RequestedAt:           ex.startedAt,
		UpstreamModel:         ex.upstreamModelID,
		Provider:              ex.accInfo.Platform,
		Stream:                stream,
		ActualCost:            cost.Total,
		Fingerprint:           ex.payloadHash,
		AuditRequestID:        ex.requestID,
		AuditRouteID:          router.TraceRouteID(ex.plan, ex.attempt),
		AuditPoolGroupID:      ex.attempt.PoolGroupID,
		AuditProviderEndpoint: ex.upstreamPath,
		Draft: gateway.UsageRecordDraft{
			TokensInput:           usage.PromptTokens,
			TokensOutput:          usage.CompletionTokens,
			DeliveredTokenCount:   int64(usage.CompletionTokens),
			ActualCost:            cost.Total,
			CostSnapshot:          cost.CostSnapshot,
			RoutingReason:         ex.selRes.RoutingReasonJSON,
			EndClass:              gateway.StreamEndGraceful,
			UsageSource:           gateway.UsageSourceReported,
			ConfidenceScore:       &confidence,
			DrainOutcome:          gateway.DrainNotDrained,
			PendingReconciliation: cost.PendingReconciliation,
			ClientTool:            clientid.ToolFromContext(ex.ctx),
		},
		EmitSchedulerOutbox: true,
		SnapshotVersion:     ex.plan.SnapshotVersion,
	}
}

// settleStreamWithRecovery 在流式响应已交付后结算,DLQ 事件源标记为流式(SourceStream)。
func (ex *execution) settleStreamWithRecovery(ctx context.Context, req billing.SettleRequest) error {
	return ex.settleWithRecovery(ctx, settlementrecovery.SourceStream, req)
}

// settleDirectWithRecovery 在非流式响应体已交付后结算,DLQ 事件源标记为非流式直结(SourceDirectSettle)。
func (ex *execution) settleDirectWithRecovery(ctx context.Context, req billing.SettleRequest) error {
	return ex.settleWithRecovery(ctx, settlementrecovery.SourceDirectSettle, req)
}

// settleWithRecovery 在响应已交付给客户端后结算。调用方必须传入**脱钩 ctx**
// (context.WithoutCancel)，使客户端断连不取消 Tx2——否则已交付 token 永不计费(计费泄漏)。
// settle 失败时把 SettleRequest 经 settlementrecovery DLQ 持久化(source 标记来路),worker 后续
// 重 settle 防钱账丢失;DLQ 重结算靠既有三证 proof(claim/usage_records/billing_events)幂等防重扣。
// SettleRecoveryDLQ 未注入或写失败时，同一份恢复证据落入 settlement_intents，
// 由 sweeper 在队列恢复后重投，不能把已交付请求永久留在 failed。
// settle err 始终原样返回 caller。
func (ex *execution) settleWithRecovery(ctx context.Context, source settlementrecovery.Source, req billing.SettleRequest) error {
	if _, err := ex.d.Settler.Settle(ctx, req); err != nil {
		payload := settlementrecovery.FromSettleRequest(source, ex.requestID, req)
		evidence, enqErr := settlementrecovery.EnqueueFailure(
			ctx,
			ex.d.SettleRecoveryDLQ,
			payload,
			err,
			"completionshttp.settle_recovery",
		)
		ex.settlementIntent.MarkSettlementResult(ctx, req.ActualCost, err, enqErr == nil, settlementintent.RecoveryEvidence{
			Payload: evidence.Payload, FailureClass: evidence.FailureClass,
		})
		if enqErr != nil {
			failureClass := privacy.ErrorClassFor(ctx, err)
			// DLQ persist 自身失败 = money path 兜底链断 → P0 alert(不阻塞:响应已发不能反悔)。
			_ = privacy.LogSystem(ctx, privacy.SystemEvent{
				Severity:   privacy.SeverityError,
				Component:  "completionshttp.settle_recovery",
				RequestID:  ex.requestID,
				ErrorClass: privacy.ErrorClassFor(ctx, enqErr),
				Attrs: map[string]any{
					"event_class":          "settle_recovery_dlq_enqueue_failed",
					"event_type":           string(source),
					"tenant_id":            req.TenantID,
					"claim_id":             req.ClaimID,
					"failure_reason_class": failureClass,
				},
			})
		}
		return err
	}
	ex.settlementIntent.MarkSettled(ctx, req.ActualCost)
	return nil
}

func (ex *execution) logResponseDeliveryUncertain(err error) {
	_ = privacy.LogSystem(ex.ctx, privacy.SystemEvent{
		Severity:   privacy.SeverityWarn,
		Component:  "completionshttp.response_delivery",
		RequestID:  ex.requestID,
		ErrorClass: privacy.ErrorClassFor(ex.ctx, err),
		Attrs: map[string]any{
			"event_class": "completion_response_delivery_uncertain",
			"tenant_id":   ex.ident.TenantID,
			"claim_id":    ex.reserveRes.ClaimID,
		},
	})
}

func (ex *execution) abort(w http.ResponseWriter, reason string, observedInputTokens int64) bool {
	return ex.abortWithError(w, reason, observedInputTokens) == nil
}

func (ex *execution) abortWithError(w http.ResponseWriter, reason string, observedInputTokens int64) error {
	if ex.reserveRes == nil {
		return nil
	}
	// 脱离请求 ctx 释放 hold 与并发槽:客户端断连时 ex.ctx 已取消,不脱离会让 Abort 失败,
	// hold/并发槽泄漏到 lease 过期才回收(与 images/rerank/embeddings/audio 的 billingCtx 同)。
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ex.ctx), 5*time.Second)
	defer cancel()
	err := ex.d.Settler.Abort(abortCtx, ex.ident.TenantID, ex.reserveRes.ClaimID, reason, ex.requestID, observedInputTokens, nil)
	ex.settlementIntent.MarkAbortResult(abortCtx, err)
	if err != nil {
		w.Header().Set("X-Huakai-Abort-Failed", clienterr.CodeAbortFailed)
		return err
	}
	ex.reserveRes = nil
	return nil
}

func (ex *execution) openDeliveryGate(w http.ResponseWriter, observedInputTokens int64) bool {
	if err := ex.settlementIntent.MarkDelivering(ex.ctx, time.Now().UTC()); err != nil {
		_ = ex.abortWithError(w, "delivery_evidence_unavailable", observedInputTokens)
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeSettleError, clienterr.MessageFor(clienterr.CodeSettleError))
		return false
	}
	return true
}

func (ex *execution) ensureIdempotency() {
	if ex.logicalRequestID != "" {
		return
	}
	ex.idempotencyKey = ex.r.Header.Get("Idempotency-Key")
	ex.logicalRequestID = ex.idempotencyKey
	if ex.logicalRequestID == "" {
		ex.logicalRequestID = uuid.NewString()
	}
}

func authoritativeAttemptSeq(res *billing.ReserveResult, fallback int) int {
	if res != nil && res.AttemptSeq > 0 {
		return int(res.AttemptSeq)
	}
	if fallback > 0 {
		return fallback
	}
	return 1
}

func (ex *execution) balanceMode() billing.BalanceEnforcementMode {
	if ex.d.BillingPolicyResolver == nil {
		return billing.DefaultBalanceEnforcementMode
	}
	return ex.d.BillingPolicyResolver.ResolveBalanceEnforcementMode(ex.ctx, ex.ident.TenantID)
}
