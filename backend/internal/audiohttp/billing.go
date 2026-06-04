package audiohttp

import (
	"encoding/json"
	"errors"
	"net/http"
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
		},
		EmitSchedulerOutbox: true,
		SnapshotVersion:     ex.plan.SnapshotVersion,
	}
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
