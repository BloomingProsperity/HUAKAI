package embeddingshttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
)

var embeddingsQuotaReserveFailedOpenTotal = expvar.NewInt("embeddings_quota_reserve_failed_open_total")

func (ex *execution) reserve(w http.ResponseWriter) bool {
	ex.ensureIdempotency()
	predictedCost, _, _, err := ex.inputCost(ex.inputEstimate)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	res, err := ex.d.ClaimGate.Reserve(ex.ctx, billing.ReserveRequest{
		TenantID:                   ex.ident.TenantID,
		APIKeyID:                   ex.ident.APIKeyID,
		UserID:                     ex.ident.UserID,
		LogicalRequestID:           ex.logicalRequestID,
		EndpointFamily:             endpointFamilyEmbeddings,
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
		writeJSONError(w, http.StatusConflict, "replay_without_cache", "idempotent embeddings request hit but stored response unavailable; retry the request")
		return false
	}
	ex.reserveRes = res
	return ex.reserveQuota(w, predictedCost)
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
		// W5:嵌入输入 token 估算喂进 token-per-window 配额预检。
		ReservedTokens: int64(ex.inputEstimate),
		PredictedCost:  predictedCost,
		At:             time.Now().UTC(),
	}))
	if err == nil && result.Allowed {
		return true
	}
	if quotaenforce.IsDenied(err) || (err == nil && !result.Allowed) {
		ex.abort(w, "quota_denied", 0)
		writeInsufficientQuotaErrorRetryable(w, quotaenforce.DenyRetryAfter(result, err), quotaenforce.DenyWindowKind(result, err))
		return false
	}
	embeddingsQuotaReserveFailedOpenTotal.Add(1)
	slog.WarnContext(ex.ctx, "quota reserve failed open",
		slog.String("request_id", ex.requestID),
		slog.Int64("tenant_id", ex.ident.TenantID),
		slog.Int64("claim_id", ex.reserveRes.ClaimID),
		slog.String("reason", "quota_reserve_infra_error"),
		slog.String("error_type", fmt.Sprintf("%T", err)),
	)
	return true
}

func (ex *execution) settleRequest(promptTokens int, cost decimal.Decimal, snapshot string, attemptSeq int, pending bool) billing.SettleRequest {
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
		Stream:            false,
		ActualCost:        cost,
		Fingerprint:       ex.payloadHash,
		AuditRequestID:    ex.requestID,
		Draft: gateway.UsageRecordDraft{
			TokensInput:           promptTokens,
			TokensOutput:          0,
			DeliveredTokenCount:   0,
			ActualCost:            cost,
			CostSnapshot:          snapshot,
			RoutingReason:         ex.selRes.RoutingReasonJSON,
			EndClass:              gateway.StreamEndGraceful,
			UsageSource:           gateway.UsageSourceReported,
			ConfidenceScore:       &confidence,
			DrainOutcome:          gateway.DrainNotDrained,
			PendingReconciliation: pending,
			ClientTool:            clientid.ToolFromContext(ex.ctx),
		},
		EmitSchedulerOutbox: true,
		SnapshotVersion:     ex.plan.SnapshotVersion,
	}
}

// billingCtx 返回脱离请求取消的结算上下文(同 audiohttp/imageshttp):客户端断连
// 不应取消已决定的扣费/退费,否则 settle 漏记收入、abort 卡住额度 hold。5s 上界防挂起。
func (ex *execution) billingCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ex.ctx), 5*time.Second)
}

func (ex *execution) abort(w http.ResponseWriter, reason string, observedInputTokens int64) {
	if ex.reserveRes == nil {
		return
	}
	bctx, cancel := ex.billingCtx()
	defer cancel()
	if err := ex.d.Settler.Abort(bctx, ex.ident.TenantID, ex.reserveRes.ClaimID, reason, ex.requestID, observedInputTokens, nil); err != nil {
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
