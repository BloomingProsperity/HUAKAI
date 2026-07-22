package dlq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UsageRecordPayload struct {
	TenantID               int64           `json:"tenant_id"`
	ClaimID                int64           `json:"claim_id"`
	APIKeyID               int64           `json:"api_key_id"`
	UserID                 int64           `json:"user_id"`
	ProviderAccountID      *int64          `json:"provider_account_id,omitempty"`
	SettlementSource       string          `json:"settlement_source"`
	AcquisitionToken       string          `json:"acquisition_token"`
	AttemptSeq             int32           `json:"attempt_seq"`
	TokensInput            int32           `json:"tokens_input"`
	TokensOutput           int32           `json:"tokens_output"`
	CacheCreationTokens    int32           `json:"cache_creation_tokens"`
	CacheReadTokens        int32           `json:"cache_read_tokens"`
	CacheCreation5mTokens  int32           `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens  int32           `json:"cache_creation_1h_tokens"`
	ImageOutputTokens      int32           `json:"image_output_tokens"`
	ActualCost             string          `json:"actual_cost"`
	CostSnapshot           *string         `json:"cost_snapshot,omitempty"`
	InputCost              string          `json:"input_cost"`
	OutputCost             string          `json:"output_cost"`
	CacheCreationCost      string          `json:"cache_creation_cost"`
	CacheReadCost          string          `json:"cache_read_cost"`
	ImageOutputCost        string          `json:"image_output_cost"`
	EndClass               string          `json:"end_class"`
	UsageSource            string          `json:"usage_source"`
	ConfidenceScore        *string         `json:"confidence_score,omitempty"`
	PendingReconciliation  bool            `json:"pending_reconciliation"`
	StreamState            int16           `json:"stream_state"`
	DeliveredTokenCount    int64           `json:"delivered_token_count"`
	StreamTerminatedReason *string         `json:"stream_terminated_reason,omitempty"`
	DrainOutcome           *string         `json:"drain_outcome,omitempty"`
	RoutingReason          json.RawMessage `json:"routing_reason"`
	ProtocolLoss           json.RawMessage `json:"protocol_loss"`
	RequestedAt            string          `json:"requested_at"`
	UpstreamRequestAt      *string         `json:"upstream_request_at,omitempty"`
	FirstByteAt            *string         `json:"first_byte_at,omitempty"`
	FirstEventAt           *string         `json:"first_event_at,omitempty"`
	LastEventAt            *string         `json:"last_event_at,omitempty"`
	RequestedModel         string          `json:"requested_model"`
	UpstreamModel          *string         `json:"upstream_model,omitempty"`
	Stream                 bool            `json:"stream"`
	SnapshotVersion        *string         `json:"snapshot_version,omitempty"`
	ImageCount             int32           `json:"image_count"`
	ImageSize              *string         `json:"image_size,omitempty"`
	ImageSizeBreakdown     json.RawMessage `json:"image_size_breakdown,omitempty"`
	IPAddress              *string         `json:"ip_address,omitempty"`
	UserAgent              *string         `json:"user_agent,omitempty"`
	ClientTool             *string         `json:"client_tool,omitempty"`
	BillingEffect          string          `json:"billing_effect,omitempty"`
}

func NewUsageRecordHandler(pool *pgxpool.Pool) Handler {
	return func(ctx context.Context, rec Record) error {
		if pool == nil {
			return ErrStoreNotConfigured
		}
		var p UsageRecordPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("dlq: decode usage payload: %w", err)
		}
		// 缓存命中 usage 记录无 acquisition_token (settlement_source=
		// response_cache_l2), payload 里为空串 → 作 NULL 重放。
		var tok any
		if p.AcquisitionToken != "" {
			parsed, parseErr := uuid.Parse(p.AcquisitionToken)
			if parseErr != nil {
				return fmt.Errorf("dlq: parse acquisition token: %w", parseErr)
			}
			tok = parsed
		}
		settlementSource := p.SettlementSource
		if settlementSource == "" {
			// 旧 DLQ payload 无此字段; migration 0043 前 usage 行全部上游路径。
			settlementSource = "provider_upstream"
		}
		billingEffect := p.BillingEffect
		if billingEffect == "" {
			billingEffect = "user_charge"
		}
		if billingEffect != "user_charge" && billingEffect != "operational_cost" {
			return fmt.Errorf("dlq: invalid billing effect %q", billingEffect)
		}
		requestedAt, err := parseTime(p.RequestedAt)
		if err != nil {
			return fmt.Errorf("dlq: parse requested_at: %w", err)
		}
		_, err = pool.Exec(ctx, `
INSERT INTO usage_records (
	tenant_id, claim_id, api_key_id, user_id, provider_account_id,
	acquisition_token, attempt_seq,
	tokens_input, tokens_output,
	cache_creation_tokens, cache_read_tokens,
	cache_creation_5m_tokens, cache_creation_1h_tokens, image_output_tokens,
	actual_cost, cost_snapshot, input_cost, output_cost,
	cache_creation_cost, cache_read_cost, image_output_cost,
	end_class, usage_source, confidence_score, pending_reconciliation,
	stream_state, delivered_token_count, stream_terminated_reason,
	drain_outcome, routing_reason, protocol_loss,
	requested_at, upstream_request_at, first_byte_at, first_event_at, last_event_at,
	requested_model, upstream_model, stream, snapshot_version, settlement_source,
	image_count, image_size, image_size_breakdown, ip_address, user_agent,
	client_tool, billing_effect
)
SELECT
	$1, $2, $3, $4, $5,
	$6, $7,
	$8, $9,
	$10, $11,
	$12, $13, $14,
	$15::numeric, $16, $17::numeric, $18::numeric,
	$19::numeric, $20::numeric, $21::numeric,
	$22, $23, $24::numeric, $25,
	$26, $27, $28,
	$29, $30, $31,
	$32, $33, $34, $35, $36,
	$37, $38, $39, $40, $41,
	$42, $43, $44, $45, $46,
	$47, $48
WHERE NOT EXISTS (
	SELECT 1 FROM usage_records WHERE tenant_id = $1 AND claim_id = $2
)`,
			p.TenantID, p.ClaimID, p.APIKeyID, p.UserID, p.ProviderAccountID,
			tok, p.AttemptSeq,
			p.TokensInput, p.TokensOutput,
			p.CacheCreationTokens, p.CacheReadTokens,
			p.CacheCreation5mTokens, p.CacheCreation1hTokens, p.ImageOutputTokens,
			p.ActualCost, p.CostSnapshot, p.InputCost, p.OutputCost,
			p.CacheCreationCost, p.CacheReadCost, p.ImageOutputCost,
			p.EndClass, p.UsageSource, p.ConfidenceScore, p.PendingReconciliation,
			usagePayloadStreamState(p), p.DeliveredTokenCount, p.StreamTerminatedReason,
			p.DrainOutcome, jsonDefault(p.RoutingReason, `{}`), jsonDefault(p.ProtocolLoss, `[]`),
			requestedAt, parseOptionalTime(p.UpstreamRequestAt), parseOptionalTime(p.FirstByteAt),
			parseOptionalTime(p.FirstEventAt), parseOptionalTime(p.LastEventAt),
			p.RequestedModel, p.UpstreamModel, p.Stream, p.SnapshotVersion, settlementSource,
			p.ImageCount, p.ImageSize, nullableJSON(p.ImageSizeBreakdown), p.IPAddress, p.UserAgent,
			p.ClientTool, billingEffect,
		)
		if err != nil {
			return fmt.Errorf("dlq: replay usage record: %w", err)
		}
		return nil
	}
}

func parseTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func usagePayloadStreamState(p UsageRecordPayload) int16 {
	if p.StreamState >= 0 && p.StreamState <= 3 {
		if p.StreamState != 0 || p.Stream || p.DeliveredTokenCount == 0 && p.TokensOutput == 0 {
			return p.StreamState
		}
	}
	if p.DeliveredTokenCount > 0 || p.TokensOutput > 0 || !p.Stream {
		return 2
	}
	return 3
}

func parseOptionalTime(raw *string) any {
	if raw == nil || *raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, *raw)
	if err != nil {
		return nil
	}
	return t.UTC()
}

func jsonDefault(v json.RawMessage, fallback string) []byte {
	if len(v) == 0 || !json.Valid(v) {
		return []byte(fallback)
	}
	return []byte(v)
}

func nullableJSON(v json.RawMessage) any {
	if len(v) == 0 || !json.Valid(v) {
		return nil
	}
	return []byte(v)
}
