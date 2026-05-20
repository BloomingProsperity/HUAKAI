//go:build integration_pg

package billing

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

func TestSettler_NilPool_ReturnsTypedError(t *testing.T) {
	settler := NewSettler(nil)
	_, err := settler.Settle(context.Background(), SettleRequest{TenantID: 1, ClaimID: 1})
	if !errors.Is(err, ErrPoolNotConfigured) {
		t.Fatalf("expected ErrPoolNotConfigured from Settle; got %v", err)
	}
	if err := settler.Abort(context.Background(), 1, 1, "test abort", "req-abort-nil-pool", 0); !errors.Is(err, ErrPoolNotConfigured) {
		t.Fatalf("expected ErrPoolNotConfigured from Abort; got %v", err)
	}
}

func TestAT_OBS_004_AtomicFiveEffect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-004")
	settler := NewSettler(pool)

	actualCost := decimal.RequireFromString("0.02000000")
	req := settleRequest(seed, actualCost)
	res, err := settler.Settle(ctx, req)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res == nil || !res.NewUserBalance.Equal(decimal.Zero) {
		t.Fatalf("NewUserBalance must be decimal.Zero in Phase B.5; got %+v", res)
	}

	var usageCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_records WHERE claim_id=$1 AND actual_cost=$2`,
		seed.claimID, actualCost,
	).Scan(&usageCount); err != nil {
		t.Fatalf("count usage_records: %v", err)
	}
	if usageCount != 1 {
		t.Fatalf("expected one usage_record with claim_id=%d; got %d", seed.claimID, usageCount)
	}

	// Slice 2 (N+5b 2026-05-01): the success-path usage row carries the
	// router+registry stamp from migration 0008's snapshot_version column.
	var snapshot *string
	if err := pool.QueryRow(ctx,
		`SELECT snapshot_version FROM usage_records WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&snapshot); err != nil {
		t.Fatalf("read snapshot_version: %v", err)
	}
	if snapshot == nil || *snapshot != "registry:99:7;router:v0.1-phase-c" {
		got := "<nil>"
		if snapshot != nil {
			got = *snapshot
		}
		t.Fatalf("usage_records.snapshot_version mismatch; got %q want %q", got, "registry:99:7;router:v0.1-phase-c")
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_committed' AND actual_cost=$2`,
		seed.claimID, actualCost,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count billing_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("expected one claim_committed billing_event; got %d", eventCount)
	}

	var status string
	var storedCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT status, actual_cost FROM billing_ledger_claims WHERE id=$1`,
		seed.claimID,
	).Scan(&status, &storedCost); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if status != "committed" || !storedCost.Equal(actualCost) {
		t.Fatalf("claim not committed with actual cost; status=%q actual_cost=%s", status, storedCost)
	}

	var inFlight int
	if err := pool.QueryRow(ctx,
		`SELECT in_flight_count FROM provider_accounts WHERE id=$1`,
		seed.providerAccountID,
	).Scan(&inFlight); err != nil {
		t.Fatalf("read in_flight_count: %v", err)
	}
	if inFlight != 1 {
		t.Fatalf("provider account in_flight_count must decrement from 2 to 1; got %d", inFlight)
	}
}

func TestSettler_AbortPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-abort")
	settler := NewSettler(pool)

	if err := settler.Abort(ctx, seed.tenantID, seed.claimID, "test abort", "req-settle-abort", 0); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	var status string
	var abortedReason *string
	if err := pool.QueryRow(ctx,
		`SELECT status, aborted_reason FROM billing_ledger_claims WHERE id=$1`,
		seed.claimID,
	).Scan(&status, &abortedReason); err != nil {
		t.Fatalf("read aborted claim: %v", err)
	}
	if status != "aborted" || abortedReason == nil || *abortedReason != "test abort" {
		t.Fatalf("claim abort fields mismatch: status=%q reason=%v", status, abortedReason)
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_aborted' AND actual_cost=0`,
		seed.claimID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count abort billing_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("expected one claim_aborted billing_event; got %d", eventCount)
	}
	var auditRequestID *string
	if err := pool.QueryRow(ctx,
		`SELECT audit_request_id FROM billing_events WHERE claim_id=$1 AND event_type='claim_aborted'`,
		seed.claimID,
	).Scan(&auditRequestID); err != nil {
		t.Fatalf("read abort billing_event audit_request_id: %v", err)
	}
	if auditRequestID == nil || *auditRequestID != "req-settle-abort" {
		t.Fatalf("abort billing_event audit_request_id=%v want req-settle-abort", auditRequestID)
	}

	var usageCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_records
		 WHERE claim_id=$1 AND actual_cost=0 AND end_class='unknown_termination'`,
		seed.claimID,
	).Scan(&usageCount); err != nil {
		t.Fatalf("count usage_records: %v", err)
	}
	if usageCount != 1 {
		t.Fatalf("abort path must write one zero-cost usage_record per T2-INV-42; got %d", usageCount)
	}
	var abortTokensInput int32
	var abortActualCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT tokens_input, actual_cost FROM usage_records WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&abortTokensInput, &abortActualCost); err != nil {
		t.Fatalf("read abort usage_record: %v", err)
	}
	if abortTokensInput != 0 || !abortActualCost.Equal(decimal.Zero) {
		t.Fatalf("default abort usage_record tokens_input=%d actual_cost=%s; want 0/0", abortTokensInput, abortActualCost)
	}

	var inFlight int
	if err := pool.QueryRow(ctx, `SELECT in_flight_count FROM provider_accounts WHERE id=$1`, seed.providerAccountID).Scan(&inFlight); err != nil {
		t.Fatalf("read in_flight_count: %v", err)
	}
	if inFlight != 1 {
		t.Fatalf("abort path must release slot + decrement in_flight_count from 2 to 1 per T2-INV-27 (slot acquired before abort); got %d", inFlight)
	}
}

func TestSettler_AbortRecordsObservedInputTokensAtZeroCost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-abort-observed-input")
	settler := NewSettler(pool)

	if err := settler.Abort(ctx, seed.tenantID, seed.claimID, "input_only_interrupted", "req-settle-abort-input", 37); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	var tokensInput int32
	var actualCost decimal.Decimal
	var inputCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT tokens_input, actual_cost, input_cost FROM usage_records WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&tokensInput, &actualCost, &inputCost); err != nil {
		t.Fatalf("read abort usage_record: %v", err)
	}
	if tokensInput != 37 {
		t.Fatalf("abort usage_record tokens_input=%d want 37", tokensInput)
	}
	if !actualCost.Equal(decimal.Zero) || !inputCost.Equal(decimal.Zero) {
		t.Fatalf("abort costs actual=%s input=%s want zero", actualCost, inputCost)
	}
}

func TestSettler_AbortCrossTenantRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-abort-xtenant")
	settler := NewSettler(pool)

	wrongTenant := seed.tenantID + 99999
	if err := settler.Abort(ctx, wrongTenant, seed.claimID, "cross-tenant", "req-settle-abort-xtenant", 0); !errors.Is(err, ErrClaimNotReserving) {
		t.Fatalf("cross-tenant abort must be rejected with ErrClaimNotReserving; got %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM billing_ledger_claims WHERE id=$1`,
		seed.claimID,
	).Scan(&status); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if status != "reserving" {
		t.Fatalf("cross-tenant abort must leave claim untouched; status=%q", status)
	}
}

func TestSettler_TokenMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-token-mismatch")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	req.AcquisitionToken = uuid.New()
	_, err := settler.Settle(ctx, req)
	if !errors.Is(err, ErrAcquisitionTokenMismatch) {
		t.Fatalf("expected ErrAcquisitionTokenMismatch; got %v", err)
	}

	var status string
	var actualCost *decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT status, actual_cost FROM billing_ledger_claims WHERE id=$1`,
		seed.claimID,
	).Scan(&status, &actualCost); err != nil {
		t.Fatalf("read claim after mismatch: %v", err)
	}
	if status != "reserving" || actualCost != nil {
		t.Fatalf("token mismatch must leave claim untouched; status=%q actual_cost=%v", status, actualCost)
	}
}

func TestSettler_AmbiguousUsageEndClassMapsToDBEnum(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-ambiguous")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	req.Draft.EndClass = gateway.AmbiguousUsage
	req.Draft.UsageSource = gateway.UsageSourceAmbiguous
	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle with AmbiguousUsage end class must map to DB enum and succeed; got %v", err)
	}

	var dbEndClass string
	if err := pool.QueryRow(ctx,
		`SELECT end_class FROM usage_records WHERE claim_id=$1`, seed.claimID,
	).Scan(&dbEndClass); err != nil {
		t.Fatalf("read usage_record end_class: %v", err)
	}
	if dbEndClass != "usage_ambiguous" {
		t.Fatalf("AmbiguousUsage gateway value must map to DB enum 'usage_ambiguous'; got %q", dbEndClass)
	}
}

func TestSettler_AlreadyCommitted_NoOp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-already-committed")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("first Settle: %v", err)
	}
	_, err := settler.Settle(ctx, req)
	if !errors.Is(err, ErrClaimNotReserving) {
		t.Fatalf("second Settle expected ErrClaimNotReserving; got %v", err)
	}
}

func TestSettler_UsageInsertFailureKeepsBillingEventAndDLQ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-usage-dlq")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	req.Draft.EndClass = gateway.StreamEndClass("bad_end_class_for_dlq")
	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle must commit billing_event + DLQ when usage insert fails; got %v", err)
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_committed'`,
		seed.claimID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count billing_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("billing_event must survive usage insert failure; got %d", eventCount)
	}

	var usageCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM usage_records WHERE claim_id=$1`, seed.claimID).Scan(&usageCount); err != nil {
		t.Fatalf("count usage_records: %v", err)
	}
	if usageCount != 0 {
		t.Fatalf("bad usage row must not be inserted; got %d", usageCount)
	}

	var dlqCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_record_dlq
		 WHERE tenant_id=$1 AND claim_id=$2 AND event_kind='usage_record'
		   AND lane='HIGH' AND status='pending'`,
		seed.tenantID, seed.claimID,
	).Scan(&dlqCount); err != nil {
		t.Fatalf("count usage_record_dlq: %v", err)
	}
	if dlqCount != 1 {
		t.Fatalf("usage insert failure must enqueue one HIGH usage_record DLQ row; got %d", dlqCount)
	}
}

func TestSettler_ReplicaIntentQueuedPrimaryStillCommits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-replica-intent")
	settler := NewSettler(pool, WithReplicaTarget("replica-test"))

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle with async replica intent: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&status); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if status != "committed" {
		t.Fatalf("primary claim must commit while replica is async; status=%q", status)
	}

	var replicaRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_record_dlq
		 WHERE tenant_id=$1 AND claim_id=$2 AND event_kind='billing_event_replica'
		   AND replica_target='replica-test' AND replica_status='pending'`,
		seed.tenantID, seed.claimID,
	).Scan(&replicaRows); err != nil {
		t.Fatalf("count replica intent: %v", err)
	}
	if replicaRows != 1 {
		t.Fatalf("expected one async replica intent; got %d", replicaRows)
	}
}

func TestAT_AUDIT_001_028_RefundQueuesBillingEventReplica(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-replica-intent")
	settler := NewSettler(pool, WithReplicaTarget("refund-replica-test"))

	if _, err := settler.Settle(ctx, settleRequest(seed, decimal.RequireFromString("0.02000000"))); err != nil {
		t.Fatalf("Settle before refund: %v", err)
	}
	refund, err := settler.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 7000,
		Reason:         "audit_mismatch",
		AuditRequestID: "req-refund-replica#audit_refund",
	})
	if err != nil {
		t.Fatalf("Refund with async replica intent: %v", err)
	}
	if refund == nil || refund.BillingEventID == 0 {
		t.Fatalf("refund result missing billing event id: %+v", refund)
	}

	var replicaRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_record_dlq
		 WHERE tenant_id=$1 AND claim_id=$2 AND event_kind='billing_event_replica'
		   AND replica_target='refund-replica-test' AND replica_status='pending'
		   AND source_table='billing_events' AND source_id=$3
		   AND payload->>'event_type'='reconciliation_appended'
		   AND payload->>'actual_cost_signed'='-0.00700000'`,
		seed.tenantID, seed.claimID, refund.BillingEventID,
	).Scan(&replicaRows); err != nil {
		t.Fatalf("count refund replica intent: %v", err)
	}
	if replicaRows != 1 {
		t.Fatalf("expected one refund replica intent; got %d", replicaRows)
	}
}

type settlerSeed struct {
	tenantID          int64
	apiKeyID          int64
	userID            int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
	claimID           int64
	acquisitionToken  uuid.UUID
	fingerprint       string
}

func seedSettlerGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) settlerSeed {
	t.Helper()
	unique := fmt.Sprintf("%s-%s", suffix, uuid.NewString())
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pool, unique)
	seed := settlerSeed{
		tenantID:         tenantID,
		apiKeyID:         apiKeyID,
		userID:           userID,
		acquisitionToken: uuid.New(),
		fingerprint:      "fingerprint-" + unique,
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM usage_records WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
	})

	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, "provider-"+unique, "Provider "+unique,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "pool-"+unique,
	).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, seed.poolGroupID, "channel-"+unique,
	).Scan(&seed.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, in_flight_count)
		 VALUES ($1, $2, $3, $4, 'api_key', 2) RETURNING id`,
		tenantID, seed.providerID, seed.channelID, "account-"+unique,
	).Scan(&seed.providerAccountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, provider_account_id, acquisition_token,
			attempt_seq, predicted_cost, currency_code, lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', $7,
			'1.0', 'standard', $8, $9,
			1, $10, 'USD', NOW() + interval '90 seconds'
		) RETURNING id`,
		tenantID, "idempotency-"+unique, seed.fingerprint, apiKeyID, userID,
		"logical-"+unique, seed.poolGroupID, seed.providerAccountID, seed.acquisitionToken,
		decimal.RequireFromString("0.01000000"),
	).Scan(&seed.claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO pool_slot_acquisitions (
			tenant_id, provider_account_id, acquisition_token, claim_id, attempt_seq, lease_expires_at
		) VALUES ($1, $2, $3, $4, 1, NOW() + interval '90 seconds')`,
		tenantID, seed.providerAccountID, seed.acquisitionToken, seed.claimID,
	); err != nil {
		t.Fatalf("seed pool slot acquisition: %v", err)
	}
	return seed
}

func settleRequest(seed settlerSeed, actualCost decimal.Decimal) SettleRequest {
	return SettleRequest{
		ClaimID:           seed.claimID,
		AccountID:         seed.providerAccountID,
		AcquisitionToken:  seed.acquisitionToken,
		ActualCost:        actualCost,
		TenantID:          seed.tenantID,
		APIKeyID:          seed.apiKeyID,
		UserID:            seed.userID,
		ProviderAccountID: seed.providerAccountID,
		AttemptSeq:        1,
		RequestedModel:    "gpt-4.1-mini",
		RequestedAt:       time.Now().UTC(),
		UpstreamModel:     "gpt-4.1-mini",
		Stream:            false,
		Fingerprint:       seed.fingerprint,
		SnapshotVersion:   "registry:99:7;router:v0.1-phase-c",
		Draft: gateway.UsageRecordDraft{
			TokensInput:           10,
			TokensOutput:          20,
			ActualCost:            actualCost,
			RoutingReason:         []byte(`{"route":"test"}`),
			EndClass:              gateway.StreamEndClass("non_streaming"),
			UsageSource:           gateway.UsageSourceReported,
			PendingReconciliation: false,
		},
	}
}
