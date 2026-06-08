package imageshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
)

func (ex *execution) reserve(w http.ResponseWriter) bool {
	ex.ensureIdempotency()
	res, err := ex.d.ClaimGate.Reserve(ex.ctx, billing.ReserveRequest{
		TenantID:                   ex.ident.TenantID,
		APIKeyID:                   ex.ident.APIKeyID,
		UserID:                     ex.ident.UserID,
		LogicalRequestID:           ex.logicalRequestID,
		EndpointFamily:             endpointFamilyImages,
		NormalizedPayloadHash:      ex.payloadHash,
		RequestedModel:             ex.req.Model,
		PoolingGroupID:             ex.attempt.PoolGroupID,
		BillingPolicyVersion:       ex.d.BillingPolicyVersion,
		RequestClass:               ex.d.RequestClass,
		PredictedCost:              ex.predictedCost,
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
		writeJSONError(w, http.StatusConflict, "replay_without_cache", "idempotent image request hit but stored response unavailable; retry the request")
		return false
	}
	ex.reserveRes = res
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
		PredictedCost:      ex.predictedCost,
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

func (ex *execution) settleRequest(tokens tokenImageUsage, cost decimal.Decimal, snapshot string, attemptSeq int, pending bool) billing.SettleRequest {
	confidence := 1.0
	imageCount := int32(ex.amount)
	imageSize := auditStringPtr(ex.size, 0)
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
			TokensInput:           tokens.InputTokens,
			TokensOutput:          tokens.OutputTokens,
			DeliveredTokenCount:   int64(tokens.OutputTokens),
			ActualCost:            cost,
			CostSnapshot:          snapshot,
			ImageCount:            imageCount,
			ImageSize:             imageSize,
			ImageSizeBreakdown:    imageSizeBreakdown(ex.size, imageCount),
			IPAddress:             auditStringPtr(ex.d.ClientIPResolver.ClientIP(ex.r), 128),
			UserAgent:             auditStringPtr(ex.r.Header.Get("User-Agent"), 512),
			RoutingReason:         ex.selRes.RoutingReasonJSON,
			EndClass:              gateway.StreamEndGraceful,
			UsageSource:           gateway.UsageSourceReported,
			ConfidenceScore:       &confidence,
			DrainOutcome:          gateway.DrainNotDrained,
			PendingReconciliation: pending,
		},
		EmitSchedulerOutbox: true,
		SnapshotVersion:     ex.plan.SnapshotVersion,
	}
}

func imageSizeBreakdown(size string, count int32) []byte {
	size = strings.TrimSpace(size)
	if size == "" || count <= 0 {
		return nil
	}
	raw, err := json.Marshal(map[string]int32{size: count})
	if err != nil {
		return nil
	}
	return raw
}

func auditStringPtr(raw string, maxRunes int) *string {
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

// billingCtx 返回脱离请求取消的结算上下文(同 audiohttp):客户端断连不应取消已决定的
// 扣费/退费,否则 settle 漏记收入、abort 卡住额度 hold。5s 上界防挂起。
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
