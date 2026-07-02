package audiohttp

import (
	"context"
	"encoding/json"
	"errors"
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
)

func (ex *execution) reserve(w http.ResponseWriter) bool {
	ex.ensureIdempotency()
	res, err := ex.d.ClaimGate.Reserve(ex.ctx, billing.ReserveRequest{
		TenantID:                   ex.ident.TenantID,
		APIKeyID:                   ex.ident.APIKeyID,
		UserID:                     ex.ident.UserID,
		LogicalRequestID:           ex.logicalRequestID,
		EndpointFamily:             endpointFamilyAudio,
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
		writeJSONError(w, http.StatusConflict, "replay_without_cache", "idempotent audio request hit but stored response unavailable; retry the request")
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

func (ex *execution) settleRequest(usage audioTokenUsage, cost decimal.Decimal, snapshot string, attemptSeq int, pending bool) billing.SettleRequest {
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
			TokensInput:           usage.InputTokens,
			TokensOutput:          usage.OutputTokens,
			DeliveredTokenCount:   int64(usage.OutputTokens),
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

// billingCtx 返回脱离请求取消的结算上下文。客户端断连不应取消"已决定"的扣费/退费:
// settle 在交付完成后被请求 ctx 取消会漏记收入,abort 被取消会让额度 hold 卡住。用
// context.WithoutCancel 摘掉取消信号,再加 5s 上界防结算调用无限挂起。
func (ex *execution) billingCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ex.ctx), 5*time.Second)
}

// logSettleAfterDeliveryFailure 在响应已完整交付后 settle 仍失败时响亮告警。此刻无法再
// 返回错误码,这条 error 级系统日志是对账发现"已交付未入账"的唯一线索(防静默漏记)。
func (ex *execution) logSettleAfterDeliveryFailure(err error) {
	bctx, cancel := ex.billingCtx()
	defer cancel()
	claimID := int64(0)
	if ex.reserveRes != nil {
		claimID = ex.reserveRes.ClaimID
	}
	_ = privacy.LogSystem(bctx, privacy.SystemEvent{
		Severity:   privacy.SeverityError,
		Component:  "audiohttp.settle_after_delivery",
		RequestID:  ex.requestID,
		ErrorClass: privacy.ErrorClassFor(bctx, err),
		Attrs: map[string]any{
			"event_class": "settle_after_delivery_failed",
			"tenant_id":   ex.ident.TenantID,
			"claim_id":    claimID,
			"endpoint":    string(ex.endpoint),
		},
	})
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

func parseTokenUsage(raw []byte) (audioTokenUsage, bool) {
	var body struct {
		Usage struct {
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return audioTokenUsage{}, false
	}
	input := firstPositive(body.Usage.InputTokens, body.Usage.PromptTokens)
	output := firstPositive(body.Usage.OutputTokens, body.Usage.CompletionTokens)
	if input <= 0 && output <= 0 {
		return audioTokenUsage{}, false
	}
	return audioTokenUsage{InputTokens: input, OutputTokens: output}, true
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
