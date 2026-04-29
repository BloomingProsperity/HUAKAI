package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

var (
	ErrClaimNotReserving        = errors.New("billing: claim is not reserving")
	ErrAcquisitionTokenMismatch = errors.New("billing: acquisition token mismatch")
	ErrSlotReleaseMissed        = errors.New("billing: pool_slot_acquisitions row not in 'acquired' state for token")
)

type DefaultSettler struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewSettler(pool *pgxpool.Pool) *DefaultSettler {
	if pool == nil {
		return &DefaultSettler{pool: nil}
	}
	return &DefaultSettler{pool: pool, q: db.New(pool)}
}

func (s *DefaultSettler) Settle(ctx context.Context, req SettleRequest) (*SettleResult, error) {
	if s == nil || s.pool == nil {
		return nil, ErrPoolNotConfigured
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("billing: begin Tx2: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := s.q.WithTx(tx)
	claim, err := qtx.GetClaimForSettle(ctx, db.GetClaimForSettleParams{
		ID:               req.ClaimID,
		TenantID:         req.TenantID,
		AcquisitionToken: req.AcquisitionToken,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, s.classifySettleNoRows(ctx, tx, req)
	}
	if err != nil {
		return nil, fmt.Errorf("billing: get claim for settle: %w", err)
	}

	providerAccountID := req.ProviderAccountID
	if providerAccountID == 0 && claim.ProviderAccountID != nil {
		providerAccountID = *claim.ProviderAccountID
	}
	if providerAccountID == 0 {
		return nil, fmt.Errorf("billing: provider account id missing for claim %d", req.ClaimID)
	}

	actualCost := req.ActualCost
	if actualCost.IsZero() && !req.Draft.ActualCost.IsZero() {
		actualCost = req.Draft.ActualCost
	}
	requestedAt := req.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	}

	// Phase B.5 v0.1: synchronous usage_record write only. Per spec
	// USAGE_RECORD_WRITE_FAIL (T2-INV-34..37): if this insert fails the whole
	// Tx2 rolls back and no audit billing_event survives. The DLQ + async
	// retry path that preserves the audit trail is DEFERRED-PHASE-4.5 per
	// docs/specs/_invariants/F-OBS-001-tx2-invariants-checklist.md and
	// docs/plans/2026-04-29-phase-b5-settler.md (out-of-scope list).
	if _, err := qtx.InsertUsageRecord(ctx, db.InsertUsageRecordParams{
		TenantID:              claim.TenantID,
		ClaimID:               claim.ID,
		APIKeyID:              coalesceInt64(req.APIKeyID, claim.APIKeyID),
		UserID:                coalesceInt64(req.UserID, claim.UserID),
		ProviderAccountID:     providerAccountID,
		AcquisitionToken:      pgUUID(req.AcquisitionToken),
		AttemptSeq:            coalesceInt32(req.AttemptSeq, claim.AttemptSeq),
		TokensInput:           int32(req.Draft.TokensInput),
		TokensOutput:          int32(req.Draft.TokensOutput),
		CacheCreationTokens:   int32(req.Draft.CacheCreationTokens),
		CacheReadTokens:       int32(req.Draft.CacheReadTokens),
		CacheCreation5mTokens: 0,
		CacheCreation1hTokens: 0,
		ImageOutputTokens:     0,
		ActualCost:            actualCost,
		InputCost:             decimal.Zero,
		OutputCost:            decimal.Zero,
		CacheCreationCost:     decimal.Zero,
		CacheReadCost:         decimal.Zero,
		ImageOutputCost:       decimal.Zero,
		EndClass:              normalizeEndClass(req.Draft.EndClass, req.Stream),
		UsageSource:           normalizeUsageSource(req.Draft.UsageSource),
		ConfidenceScore:       numericFromFloat(req.Draft.ConfidenceScore),
		PendingReconciliation: req.Draft.PendingReconciliation,
		DrainOutcome:          normalizeDrainOutcome(req.Draft.DrainOutcome),
		RoutingReason:         jsonOrEmptyObject(req.Draft.RoutingReason),
		ProtocolLoss:          []byte("[]"),
		RequestedAt:           pgTimestamp(requestedAt),
		RequestedModel:        coalesceString(req.RequestedModel, claim.RequestedModel),
		UpstreamModel:         nullableString(req.UpstreamModel),
		Stream:                req.Stream,
	}); err != nil {
		return nil, fmt.Errorf("billing: insert usage record: %w", err)
	}

	endClass := normalizeEndClass(req.Draft.EndClass, req.Stream)
	usageSource := normalizeUsageSource(req.Draft.UsageSource)
	if _, err := qtx.InsertBillingEvent(ctx, db.InsertBillingEventParams{
		TenantID:         claim.TenantID,
		ClaimID:          claim.ID,
		EventType:        "claim_committed",
		ActualCost:       actualCost,
		ActualCostSigned: actualCost,
		EndClass:         &endClass,
		UsageSource:      &usageSource,
		Fingerprint:      coalesceString(req.Fingerprint, claim.RequestFingerprint),
	}); err != nil {
		return nil, fmt.Errorf("billing: insert billing event: %w", err)
	}

	// TODO: outbox emission deferred until Phase 4.5; CrossThreshold callback hook is in SettleRequest.OutboxEmitter func() bool - when true, an outbox row is inserted.
	outboxEvents := 0
	if req.OutboxEmitter != nil && req.OutboxEmitter() {
		if _, err := qtx.InsertSchedulerOutboxRow(ctx, db.InsertSchedulerOutboxRowParams{
			TenantID:          claim.TenantID,
			EventType:         "account_quota_changed",
			ProviderAccountID: &providerAccountID,
			Payload:           []byte("{}"),
		}); err != nil {
			return nil, fmt.Errorf("billing: insert scheduler outbox row: %w", err)
		}
		outboxEvents = 1
	}

	releaseReason := "settled_committed"
	released, err := qtx.ReleaseSlotAndDecrementInFlight(ctx, db.ReleaseSlotAndDecrementInFlightParams{
		AcquisitionToken: req.AcquisitionToken,
		ReleaseReason:    &releaseReason,
	})
	if err != nil {
		return nil, fmt.Errorf("billing: release slot + decrement in-flight count: %w", err)
	}
	if released == 0 {
		return nil, ErrSlotReleaseMissed
	}
	rows, err := qtx.UpdateClaimCommitted(ctx, db.UpdateClaimCommittedParams{
		ID: claim.ID,
		ActualCost: decimal.NullDecimal{
			Decimal: actualCost,
			Valid:   true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("billing: update claim committed: %w", err)
	}
	if rows == 0 {
		return nil, ErrClaimNotReserving
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("billing: commit Tx2: %w", err)
	}
	return &SettleResult{NewUserBalance: decimal.Zero, OutboxEventsEnqueued: outboxEvents}, nil
}

func (s *DefaultSettler) Abort(ctx context.Context, tenantID, claimID int64, reason string) error {
	if s == nil || s.pool == nil {
		return ErrPoolNotConfigured
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("billing: begin abort Tx2: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fingerprint string
	var status string
	var acquisitionToken pgtype.UUID
	var apiKeyID, userID int64
	var providerAccountID *int64
	var attemptSeq int32
	var requestedModel string
	if err := tx.QueryRow(ctx,
		`SELECT request_fingerprint, status, acquisition_token, api_key_id, user_id,
		        provider_account_id, attempt_seq, requested_model
		 FROM billing_ledger_claims WHERE id=$1 AND tenant_id=$2 FOR UPDATE`,
		claimID, tenantID,
	).Scan(&fingerprint, &status, &acquisitionToken, &apiKeyID, &userID,
		&providerAccountID, &attemptSeq, &requestedModel); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrClaimNotReserving
		}
		return fmt.Errorf("billing: get claim for abort: %w", err)
	}
	if status != "reserving" {
		return ErrClaimNotReserving
	}

	qtx := s.q.WithTx(tx)
	rows, err := qtx.UpdateClaimAbortedWithReason(ctx, db.UpdateClaimAbortedWithReasonParams{
		ID:            claimID,
		TenantID:      tenantID,
		AbortedReason: nullableString(reason),
	})
	if err != nil {
		return fmt.Errorf("billing: update claim aborted: %w", err)
	}
	if rows == 0 {
		return ErrClaimNotReserving
	}
	abortEndClass := "unknown_termination"
	abortUsageSource := string(gateway.UsageSourceInferred)
	if _, err := qtx.InsertBillingEvent(ctx, db.InsertBillingEventParams{
		TenantID:         tenantID,
		ClaimID:          claimID,
		EventType:        "claim_aborted",
		ActualCost:       decimal.Zero,
		ActualCostSigned: decimal.Zero,
		EndClass:         &abortEndClass,
		UsageSource:      &abortUsageSource,
		Fingerprint:      fingerprint,
	}); err != nil {
		return fmt.Errorf("billing: insert abort billing event: %w", err)
	}

	// Audit-grade Usage Record on abort path (T2-INV-42): every Tx2 commit,
	// including aborted final disposition, produces a usage_record so aborts
	// remain queryable consistently with committed requests.
	// Only writable when Pool wrote back provider_account_id (NOT NULL on
	// usage_records); pre-acquire aborts skip the record.
	if providerAccountID != nil && acquisitionToken.Valid {
		var tokAbort uuid.UUID
		copy(tokAbort[:], acquisitionToken.Bytes[:])
		if _, err := qtx.InsertUsageRecord(ctx, db.InsertUsageRecordParams{
			TenantID:              tenantID,
			ClaimID:               claimID,
			APIKeyID:              apiKeyID,
			UserID:                userID,
			ProviderAccountID:     *providerAccountID,
			AcquisitionToken:      pgUUID(tokAbort),
			AttemptSeq:            attemptSeq,
			ActualCost:            decimal.Zero,
			InputCost:             decimal.Zero,
			OutputCost:            decimal.Zero,
			CacheCreationCost:     decimal.Zero,
			CacheReadCost:         decimal.Zero,
			ImageOutputCost:       decimal.Zero,
			EndClass:              abortEndClass,
			UsageSource:           abortUsageSource,
			PendingReconciliation: false,
			RoutingReason:         []byte("{}"),
			ProtocolLoss:          []byte("[]"),
			RequestedAt:           pgTimestamp(time.Now().UTC()),
			RequestedModel:        requestedModel,
		}); err != nil {
			return fmt.Errorf("billing: insert abort usage record: %w", err)
		}
	}

	// Idempotently release pool slot + decrement in_flight if Pool wrote back
	// the acquisition_token (Pattern B). When the claim aborts BEFORE Pool
	// acquired (token NULL), there's nothing to release.
	if acquisitionToken.Valid {
		releaseReason := "settled_aborted"
		var tokUUID uuid.UUID
		copy(tokUUID[:], acquisitionToken.Bytes[:])
		released, err := qtx.ReleaseSlotAndDecrementInFlight(ctx, db.ReleaseSlotAndDecrementInFlightParams{
			AcquisitionToken: tokUUID,
			ReleaseReason:    &releaseReason,
		})
		if err != nil {
			return fmt.Errorf("billing: release slot on abort: %w", err)
		}
		if released == 0 {
			return ErrSlotReleaseMissed
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("billing: commit abort Tx2: %w", err)
	}
	return nil
}

func (s *DefaultSettler) classifySettleNoRows(ctx context.Context, tx pgx.Tx, req SettleRequest) error {
	var token pgtype.UUID
	var status string
	if err := tx.QueryRow(ctx,
		`SELECT acquisition_token, status FROM billing_ledger_claims WHERE id=$1 AND tenant_id=$2 FOR UPDATE`,
		req.ClaimID, req.TenantID,
	).Scan(&token, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrClaimNotReserving
		}
		return fmt.Errorf("billing: classify claim settle failure: %w", err)
	}
	if status != "reserving" {
		return ErrClaimNotReserving
	}
	if !token.Valid || token.Bytes != req.AcquisitionToken {
		return ErrAcquisitionTokenMismatch
	}
	return ErrClaimNotReserving
}

func pgUUID(v uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: v, Valid: true}
}

func pgTimestamp(v time.Time) pgtype.Timestamptz {
	if v.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: v.UTC(), Valid: true}
}

func numericFromFloat(v *float64) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	n := pgtype.Numeric{}
	_ = n.Scan(decimal.NewFromFloat(*v).String())
	return n
}

func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func coalesceString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func coalesceInt64(v, fallback int64) int64 {
	if v != 0 {
		return v
	}
	return fallback
}

func coalesceInt32(v, fallback int32) int32 {
	if v != 0 {
		return v
	}
	return fallback
}

func jsonOrEmptyObject(v []byte) []byte {
	if len(v) == 0 {
		return []byte("{}")
	}
	return v
}

func normalizeUsageSource(v gateway.UsageSource) string {
	if v == "" {
		return "reported"
	}
	return string(v)
}

func normalizeEndClass(v gateway.StreamEndClass, stream bool) string {
	switch v {
	case "":
		if stream {
			return "unknown_termination"
		}
		return "non_streaming"
	case gateway.UpstreamEOFNoTerminal:
		return "stream_end_no_terminal_marker"
	case gateway.ResponseEventTooLarge:
		return "event_size_exceeded"
	case gateway.OrchestratorCancel:
		return "orchestrator_cancelled"
	case gateway.AmbiguousUsage:
		return "usage_ambiguous"
	default:
		return string(v)
	}
}

func normalizeDrainOutcome(v gateway.DrainOutcome) *string {
	var out string
	switch v {
	case "":
		return nil
	case gateway.DrainBudgetSecondsExhausted:
		out = "max_seconds"
	case gateway.DrainBudgetBytesExhausted:
		out = "max_bytes"
	case gateway.DrainBudgetCostExhausted:
		out = "max_estimated_cost"
	case gateway.DrainNotDrained:
		return nil
	default:
		out = string(v)
	}
	return &out
}

var _ Settler = (*DefaultSettler)(nil)
