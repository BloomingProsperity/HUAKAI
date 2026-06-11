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
	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/imagepricing"
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

// billableImageCount 是 per_image 计费/审计的真相源:常态返回请求 amount,
// 但当 family 响应翻译记录的实际交付张数少于请求数时(Replicate model-specific
// num_outputs 被忽略),返回交付数——成本与 ImageCount 审计都按交付数走,
// 不按请求数多收。交付数为 0(未翻译/解析失败)回退请求数,保守不少收。
func (ex *execution) billableImageCount() int {
	if ex.scheme == imagepricing.SchemePerImage && ex.deliveredImageCount > 0 && ex.deliveredImageCount < ex.amount {
		return ex.deliveredImageCount
	}
	return ex.amount
}

func (ex *execution) settleRequest(tokens tokenImageUsage, cost decimal.Decimal, snapshot string, attemptSeq int, pending bool) billing.SettleRequest {
	confidence := 1.0
	imageCount := int32(ex.billableImageCount())
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
			ClientTool:            clientid.ToolFromContext(ex.ctx),
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
	ex.abortWithLoss(w, reason, observedInputTokens, nil)
}

// abortWithLoss 在 abort 时把 protocol_loss 审计证据(如 replicate prediction id
// 与 cancel 结局)一并落 usage_records,供事后对账上游账单。
func (ex *execution) abortWithLoss(w http.ResponseWriter, reason string, observedInputTokens int64, protocolLoss json.RawMessage) {
	if ex.reserveRes == nil {
		return
	}
	bctx, cancel := ex.billingCtx()
	defer cancel()
	if err := ex.d.Settler.Abort(bctx, ex.ident.TenantID, ex.reserveRes.ClaimID, reason, ex.requestID, observedInputTokens, protocolLoss); err != nil {
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
