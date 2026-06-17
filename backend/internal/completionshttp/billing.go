package completionshttp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
)

func (ex *execution) reserve(w http.ResponseWriter) bool {
	ex.ensureIdempotency()
	predicted, err := ex.inputCost(ex.inputEstimate)
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
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeReserveError, clienterr.MessageFor(clienterr.CodeReserveError))
		return false
	}
	if res.IdempotencyHit {
		writeJSONError(w, http.StatusConflict, "replay_without_cache", "idempotent completions request hit but stored response unavailable; retry the request")
		return false
	}
	ex.reserveRes = res
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
		PredictedCost:      predictedCost,
		At:                 time.Now().UTC(),
	}))
	if err == nil && result.Allowed {
		return true
	}
	if quotaenforce.IsDenied(err) || (err == nil && !result.Allowed) {
		ex.abort(w, "quota_denied", 0)
		writeInsufficientQuotaError(w)
		return false
	}
	return true
}

func (ex *execution) settleRequest(usage completionUsage, cost completionCostBreakdown, attemptSeq int, stream bool) billing.SettleRequest {
	confidence := 1.0
	return billing.SettleRequest{
		ClaimID:           ex.reserveRes.ClaimID,
		AccountID:         ex.selRes.AccountID,
		AcquisitionToken:  ex.selRes.AcquisitionToken,
		TenantID:          ex.ident.TenantID,
		APIKeyID:          ex.ident.APIKeyID,
		UserID:            ex.ident.UserID,
		ProviderAccountID: ex.selRes.AccountID,
		AttemptSeq:        int32(attemptSeq),
		RequestedModel:    ex.req.Model,
		RequestedAt:       ex.startedAt,
		UpstreamModel:     ex.upstreamModelID,
		Provider:          ex.accInfo.Platform,
		Stream:            stream,
		ActualCost:        cost.Total,
		Fingerprint:       ex.payloadHash,
		AuditRequestID:    ex.requestID,
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
		},
		EmitSchedulerOutbox: true,
		SnapshotVersion:     ex.plan.SnapshotVersion,
	}
}

// settleStreamWithRecovery 在流式响应已交付给客户端后结算。调用方必须传入**脱钩 ctx**
// (context.WithoutCancel)，使客户端断连不取消 Tx2——否则已交付 token 永不计费(S1-2 计费泄漏)。
// settle 失败时把 SettleRequest 经 settlementrecovery DLQ 持久化(SourceStream)，worker 后续
// 重 settle 防钱账丢失；DLQ 重结算靠既有三证 proof(claim/usage_records/billing_events)幂等防重扣。
// 镜像 gatewayhttp.settleCompletionWithRecovery 的交付后兜底语义(S1-3 补齐 completions 路径缺的保护)。
// SettleRecoveryDLQ 未注入时退回原行为(仅返 err，caller 置 X-Huakai-Settle-Failed 头)。
// settle err 始终原样返回 caller。
func (ex *execution) settleStreamWithRecovery(ctx context.Context, req billing.SettleRequest) error {
	if _, err := ex.d.Settler.Settle(ctx, req); err != nil {
		if ex.d.SettleRecoveryDLQ == nil {
			return err
		}
		payload := settlementrecovery.FromSettleRequest(settlementrecovery.SourceStream, ex.requestID, req)
		failureClass := privacy.ErrorClassFor(ctx, err)
		// DLQ 持久化必须用**独立 ctx**：settle 可能正因传入 ctx 的 deadline 耗尽(DB 锁等待/上游慢)
		// 而失败，此刻复用同一已过期 ctx 会让 enqueue 的 INSERT 立即 deadline-exceeded、recovery
		// intent 落不了盘——DB 受压时 DLQ 最该兜底却最易失效。WithoutCancel 去掉过期/取消传播后
		// 重新 WithTimeout，使 enqueue 不受 settle ctx 状态影响。
		enqCtx, enqCancel := context.WithTimeout(context.WithoutCancel(ctx), settleRecoveryEnqueueTimeout)
		defer enqCancel()
		if _, enqErr := settlementrecovery.EnqueuePayload(enqCtx, ex.d.SettleRecoveryDLQ, payload, failureClass); enqErr != nil {
			// DLQ persist 自身失败 = money path 兜底链断 → P0 alert(不阻塞:流式响应已发不能反悔)。
			_ = privacy.LogSystem(enqCtx, privacy.SystemEvent{
				Severity:   privacy.SeverityError,
				Component:  "completionshttp.settle_recovery",
				RequestID:  ex.requestID,
				ErrorClass: privacy.ErrorClassFor(ctx, enqErr),
				Attrs: map[string]any{
					"event_class":          "settle_recovery_dlq_enqueue_failed",
					"event_type":           string(settlementrecovery.SourceStream),
					"tenant_id":            req.TenantID,
					"claim_id":             req.ClaimID,
					"failure_reason_class": failureClass,
				},
			})
		}
		return err
	}
	return nil
}

func (ex *execution) abort(w http.ResponseWriter, reason string, observedInputTokens int64) {
	if ex.reserveRes == nil {
		return
	}
	if err := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, reason, ex.requestID, observedInputTokens, nil); err != nil {
		w.Header().Set("X-Huakai-Abort-Failed", clienterr.CodeAbortFailed)
	}
}

func (ex *execution) ensureIdempotency() {
	ex.idempotencyKey = ex.r.Header.Get("Idempotency-Key")
	ex.logicalRequestID = ex.idempotencyKey
	if ex.logicalRequestID == "" {
		ex.logicalRequestID = uuid.NewString()
	}
}

func (ex *execution) balanceMode() billing.BalanceEnforcementMode {
	if ex.d.BillingPolicyResolver == nil {
		return billing.DefaultBalanceEnforcementMode
	}
	return ex.d.BillingPolicyResolver.ResolveBalanceEnforcementMode(ex.ctx, ex.ident.TenantID)
}
