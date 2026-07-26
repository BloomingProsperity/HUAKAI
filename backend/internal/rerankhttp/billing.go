package rerankhttp

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

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

var rerankQuotaReserveFailedOpenTotal = expvar.NewInt("rerank_quota_reserve_failed_open_total")

func (ex *execution) reserve(w http.ResponseWriter) bool {
	ex.ensureIdempotency()
	predictedCost, costSnapshot, pending, err := ex.searchUnitCost(ex.searchUnits)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	ex.predictedCost = predictedCost
	ex.costSnapshot = costSnapshot
	ex.pending = pending
	res, err := ex.d.ClaimGate.Reserve(ex.ctx, billing.ReserveRequest{
		TenantID:                   ex.ident.TenantID,
		APIKeyID:                   ex.ident.APIKeyID,
		UserID:                     ex.ident.UserID,
		LogicalRequestID:           ex.logicalRequestID,
		EndpointFamily:             endpointFamilyRerank,
		NormalizedPayloadHash:      ex.payloadHash,
		RequestedModel:             ex.req.Model,
		PoolingGroupID:             ex.attempt.PoolGroupID,
		BillingPolicyVersion:       ex.d.BillingPolicyVersion,
		RequestClass:               ex.d.RequestClass,
		PredictedCost:              predictedCost,
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
		writeJSONError(w, http.StatusConflict, "replay_without_cache", "idempotent rerank request hit but stored response unavailable; retry the request")
		return false
	}
	ex.reserveRes = res
	if err := ex.settlementIntent.InsertPending(ex.ctx, ex.ident.TenantID, ex.requestID, ex.logicalRequestID, res.ClaimID, res.AttemptSeq, ex.ident.APIKeyID, ex.payloadHash, predictedCost); err != nil {
		_ = ex.abortWithError(w, "settlement_intent_unavailable", 0)
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeSettleError, clienterr.MessageFor(clienterr.CodeSettleError))
		return false
	}
	return ex.reserveQuota(w)
}

func (ex *execution) reserveQuota(w http.ResponseWriter) bool {
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
		PredictedCost:      ex.predictedCost,
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
	rerankQuotaReserveFailedOpenTotal.Add(1)
	slog.WarnContext(ex.ctx, "quota reserve failed open",
		slog.String("request_id", ex.requestID),
		slog.Int64("tenant_id", ex.ident.TenantID),
		slog.Int64("claim_id", ex.reserveRes.ClaimID),
		slog.String("reason", "quota_reserve_infra_error"),
		slog.String("error_type", fmt.Sprintf("%T", err)),
	)
	return true
}

func (ex *execution) settleRequest(costSnapshot string, attemptSeq int) billing.SettleRequest {
	confidence := 0.5
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
		Stream:                false,
		ActualCost:            ex.predictedCost,
		Fingerprint:           ex.payloadHash,
		AuditRequestID:        ex.requestID,
		AuditRouteID:          router.TraceRouteID(ex.plan, ex.attempt),
		AuditPoolGroupID:      ex.attempt.PoolGroupID,
		AuditProviderEndpoint: upstreamRerankPath,
		Draft: gateway.UsageRecordDraft{
			TokensInput:           ex.inputEstimate,
			TokensOutput:          0,
			DeliveredTokenCount:   0,
			ActualCost:            ex.predictedCost,
			CostSnapshot:          costSnapshot,
			RoutingReason:         ex.selRes.RoutingReasonJSON,
			EndClass:              gateway.StreamEndGraceful,
			UsageSource:           gateway.UsageSourceInferred,
			ConfidenceScore:       &confidence,
			DrainOutcome:          gateway.DrainNotDrained,
			PendingReconciliation: ex.pending,
			ClientTool:            clientid.ToolFromContext(ex.ctx),
		},
		EmitSchedulerOutbox: true,
		SnapshotVersion:     ex.plan.SnapshotVersion,
	}
}

func (ex *execution) settleDeliveredResponse(ctx context.Context, req billing.SettleRequest) error {
	if _, err := ex.d.Settler.Settle(ctx, req); err != nil {
		failureClass := privacy.ErrorClassFor(ctx, err)
		payload := settlementrecovery.FromSettleRequest(settlementrecovery.SourceRerankDelivered, ex.requestID, req)
		evidence, enqueueErr := settlementrecovery.EnqueueFailure(ctx, ex.d.SettleRecoveryDLQ, payload, err, "rerankhttp.settle_recovery")
		ex.settlementIntent.MarkSettlementResult(ctx, req.ActualCost, err, enqueueErr == nil, settlementintent.RecoveryEvidence{
			Payload: evidence.Payload, FailureClass: evidence.FailureClass,
		})
		_ = privacy.LogSystem(ctx, privacy.SystemEvent{
			Severity: privacy.SeverityError, Component: "rerankhttp.settle_recovery",
			RequestID: ex.requestID, ErrorClass: failureClass, Attrs: map[string]any{
				"event_class": "rerank_settlement_deferred", "tenant_id": req.TenantID,
				"claim_id": req.ClaimID, "failure_reason_class": failureClass,
			},
		})
		return err
	}
	ex.settlementIntent.MarkSettled(ctx, req.ActualCost)
	return nil
}

// billingCtx 返回脱离请求取消的结算上下文(同 audiohttp/imageshttp):客户端断连
// 不应取消已决定的扣费/退费,否则 settle 漏记收入、abort 卡住额度 hold。5s 上界防挂起。
func (ex *execution) billingCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ex.ctx), 5*time.Second)
}

func (ex *execution) abort(w http.ResponseWriter, reason string, observedInputTokens int64) bool {
	return ex.abortWithError(w, reason, observedInputTokens) == nil
}

func (ex *execution) abortWithError(w http.ResponseWriter, reason string, observedInputTokens int64) error {
	if ex.reserveRes == nil {
		return nil
	}
	bctx, cancel := ex.billingCtx()
	defer cancel()
	err := ex.d.Settler.Abort(bctx, ex.ident.TenantID, ex.reserveRes.ClaimID, reason, ex.requestID, observedInputTokens, nil)
	ex.settlementIntent.MarkAbortResult(bctx, err)
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
