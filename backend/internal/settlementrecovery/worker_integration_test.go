//go:build integration_pg

package settlementrecovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

type countingSettler struct {
	inner       billing.Settler
	settleCalls int
}

func (s *countingSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.settleCalls++
	return s.inner.Settle(ctx, req)
}

func (s *countingSettler) Abort(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64, protocolLoss json.RawMessage) error {
	return s.inner.Abort(ctx, tenantID, claimID, reason, auditRequestID, observedInputTokens, protocolLoss)
}

func (s *countingSettler) CommitCacheHit(ctx context.Context, req billing.SettleRequest) error {
	return s.inner.CommitCacheHit(ctx, req)
}

func (s *countingSettler) Refund(ctx context.Context, req billing.RefundRequest) (*billing.RefundResult, error) {
	return s.inner.Refund(ctx, req)
}

type recoveryMoneySeed struct {
	tenantID         int64
	apiKeyID         int64
	userID           int64
	providerAccount  int64
	claimID          int64
	acquisitionToken uuid.UUID
	fingerprint      string
}

// TestWorker_MissingAuditEvidenceKeepsPendingAndHoldUntouched 守住恢复前审计重验。
// 变异：删除 Handler.ValidateAuditRef 调用后，真实 Settler 会提交 claim、扣余额并把行标 delivered。
func TestWorker_MissingAuditEvidenceKeepsPendingAndHoldUntouched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRecoveryPool(t, ctx)
	seed := seedRecoveryMoneyGraph(t, ctx, pool, "missing-audit", "chat")
	reserveRecoveryHold(t, ctx, pool, seed, decimal.RequireFromString("0.01000000"))

	settler := &countingSettler{inner: billing.NewSettler(pool)}
	handler := &Handler{
		Settler: settler,
		Proof:   NewPostgresCommittedProof(pool),
		AuditRefPolicy: &eventbus.AuditRefPolicy{
			ReleaseMode: eventbus.ReleaseModeProduction,
		},
	}
	store := dlq.NewStore(pool)
	service := dlq.NewService(store, dlq.WithPolicy(dlq.RetryPolicy{
		BaseBackoff: time.Second,
		CapBackoff:  time.Second,
		MaxAttempts: 5,
		DLQAfter:    time.Hour,
	}))
	service.Register(dlq.EventKindPostDeliverySettlement, handler.Handle)
	req := recoverySettleRequest(seed, decimal.RequireFromString("0.03000000"), false)
	payload := FromCompletionEvent(SourceStream, eventbus.RequestCompletionEvent{
		ID:            "evt-missing-audit-" + uuid.NewString(),
		TenantID:      seed.tenantID,
		ClaimID:       seed.claimID,
		RequestID:     req.AuditRequestID,
		SettleRequest: req,
	})
	id, err := EnqueuePayload(ctx, service, payload, "settle failed before audit proof")
	if err != nil {
		t.Fatalf("EnqueuePayload: %v", err)
	}

	worker := dlq.NewWorker(service, dlq.WorkerConfig{HighWorkers: 1, LeaseTTL: time.Second})
	processed, err := worker.RunOnce(ctx, dlq.LaneHigh, "missing-audit-worker")
	if err != nil || !processed {
		t.Fatalf("RunOnce processed/err=%v/%v want true/nil", processed, err)
	}
	if settler.settleCalls != 0 {
		t.Fatalf("Settler.Settle calls=%d want 0", settler.settleCalls)
	}

	var dlqStatus, claimStatus string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, replay_attempts FROM usage_record_dlq WHERE id=$1`, id,
	).Scan(&dlqStatus, &attempts); err != nil {
		t.Fatalf("read recovery row: %v", err)
	}
	if dlqStatus != string(dlq.StatusPending) || attempts != 1 {
		t.Fatalf("recovery status/attempts=%s/%d want pending/1", dlqStatus, attempts)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM billing_ledger_claims WHERE tenant_id=$1 AND id=$2`, seed.tenantID, seed.claimID,
	).Scan(&claimStatus); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if claimStatus != "reserving" {
		t.Fatalf("claim status=%q want reserving", claimStatus)
	}
	assertRecoveryBalance(t, ctx, pool, seed, "10.00000000", "0.01000000")
	assertRecoveryEvidenceCounts(t, ctx, pool, seed, 0, 0)
}

// TestWorker_ImagesDeliveredSettlesAndMarksDelivered 覆盖图片恢复的真实消费全链：
// Claim→Handler→Settler.Settle→MarkDelivered，并核对余额、hold、usage 与 billing event 金额。
func TestWorker_ImagesDeliveredSettlesAndMarksDelivered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRecoveryPool(t, ctx)
	seed := seedRecoveryMoneyGraph(t, ctx, pool, "images-delivered", "images")
	reserveRecoveryHold(t, ctx, pool, seed, decimal.RequireFromString("0.01000000"))

	settler := &countingSettler{inner: billing.NewSettler(pool)}
	handler := &Handler{
		Settler: settler,
		Proof:   NewPostgresCommittedProof(pool),
		AuditRefPolicy: &eventbus.AuditRefPolicy{
			ReleaseMode: eventbus.ReleaseModeProduction,
		},
	}
	store := dlq.NewStore(pool)
	service := dlq.NewService(store)
	service.Register(dlq.EventKindPostDeliverySettlement, handler.Handle)
	actualCost := decimal.RequireFromString("0.03000000")
	req := recoverySettleRequest(seed, actualCost, true)
	payload := FromSettleRequest(SourceImagesDelivered, req.AuditRequestID, req)
	id, err := EnqueuePayload(ctx, service, payload, "image settlement failed")
	if err != nil {
		t.Fatalf("EnqueuePayload: %v", err)
	}

	worker := dlq.NewWorker(service, dlq.WorkerConfig{HighWorkers: 1, LeaseTTL: time.Second})
	processed, err := worker.RunOnce(ctx, dlq.LaneHigh, "images-delivered-worker")
	if err != nil || !processed {
		t.Fatalf("RunOnce processed/err=%v/%v want true/nil", processed, err)
	}
	if settler.settleCalls != 1 {
		t.Fatalf("Settler.Settle calls=%d want 1", settler.settleCalls)
	}

	var dlqStatus, claimStatus string
	var claimCost decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT status FROM usage_record_dlq WHERE id=$1`, id).Scan(&dlqStatus); err != nil {
		t.Fatalf("read recovery row: %v", err)
	}
	if dlqStatus != string(dlq.StatusDelivered) {
		t.Fatalf("recovery status=%q want delivered", dlqStatus)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status, actual_cost FROM billing_ledger_claims WHERE tenant_id=$1 AND id=$2`, seed.tenantID, seed.claimID,
	).Scan(&claimStatus, &claimCost); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if claimStatus != "committed" || !claimCost.Equal(actualCost) {
		t.Fatalf("claim status/cost=%s/%s want committed/%s", claimStatus, claimCost, actualCost)
	}
	assertRecoveryBalance(t, ctx, pool, seed, "9.97000000", "0.00000000")
	assertRecoveryEvidenceCounts(t, ctx, pool, seed, 1, 1)

	var usageCost decimal.Decimal
	var imageCount int32
	if err := pool.QueryRow(ctx,
		`SELECT actual_cost, image_count FROM usage_records WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, seed.claimID,
	).Scan(&usageCost, &imageCount); err != nil {
		t.Fatalf("read usage record: %v", err)
	}
	if !usageCost.Equal(actualCost) || imageCount != 2 {
		t.Fatalf("usage cost/image_count=%s/%d want %s/2", usageCost, imageCount, actualCost)
	}
	var eventCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT actual_cost FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='claim_committed'`, seed.tenantID, seed.claimID,
	).Scan(&eventCost); err != nil {
		t.Fatalf("read billing event: %v", err)
	}
	if !eventCost.Equal(actualCost) {
		t.Fatalf("billing event cost=%s want %s", eventCost, actualCost)
	}
}

func openRecoveryPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedRecoveryMoneyGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix, endpointFamily string) recoveryMoneySeed {
	t.Helper()
	unique := suffix + "-" + uuid.NewString()
	seed := recoveryMoneySeed{
		acquisitionToken: uuid.New(),
		fingerprint:      "fingerprint-" + unique,
	}
	var providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "recovery-tenant-"+unique).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM usage_records WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM balance_holds WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM channels WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM pool_groups WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM providers WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM user_balances WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`, seed.tenantID, "recovery-user-"+unique,
	).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, '$2a$10$recovery-placeholder', $4, 'active') RETURNING id`,
		seed.tenantID, seed.userID, "recovery-key-"+unique, "hk_recovery_"+suffix,
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0)`, seed.tenantID, seed.userID,
	); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`, seed.tenantID, "provider-"+unique, "Provider "+unique,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, seed.tenantID, "pool-"+unique,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, seed.tenantID, poolGroupID, "channel-"+unique,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, in_flight_count)
		 VALUES ($1, $2, $3, $4, 'api_key', 1) RETURNING id`, seed.tenantID, providerID, channelID, "account-"+unique,
	).Scan(&seed.providerAccount); err != nil {
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
			$6, $7, 'gpt-image-1', $8,
			'1.0', 'standard', $9, $10,
			1, 0.01, 'USD', NOW() + interval '90 seconds'
		 ) RETURNING id`,
		seed.tenantID, "idem-"+unique, seed.fingerprint, seed.apiKeyID, seed.userID,
		"logical-"+unique, endpointFamily, poolGroupID, seed.providerAccount, seed.acquisitionToken,
	).Scan(&seed.claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO pool_slot_acquisitions (
			tenant_id, provider_account_id, acquisition_token, claim_id, attempt_seq, lease_expires_at
		 ) VALUES ($1, $2, $3, $4, 1, NOW() + interval '90 seconds')`,
		seed.tenantID, seed.providerAccount, seed.acquisitionToken, seed.claimID,
	); err != nil {
		t.Fatalf("seed slot: %v", err)
	}
	return seed
}

func reserveRecoveryHold(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed recoveryMoneySeed, cost decimal.Decimal) {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatalf("begin hold tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := billing.Reserve(ctx, tx, billing.ReserveParams{
		TenantID: seed.tenantID,
		UserID:   seed.userID,
		ClaimID:  seed.claimID,
		Cost:     cost,
	}); err != nil {
		t.Fatalf("reserve hold: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit hold: %v", err)
	}
}

func recoverySettleRequest(seed recoveryMoneySeed, actualCost decimal.Decimal, images bool) billing.SettleRequest {
	confidence := 1.0
	draft := gateway.UsageRecordDraft{
		TokensInput:           10,
		TokensOutput:          20,
		DeliveredTokenCount:   20,
		ActualCost:            actualCost,
		EndClass:              gateway.StreamEndGraceful,
		UsageSource:           gateway.UsageSourceReported,
		ConfidenceScore:       &confidence,
		DrainOutcome:          gateway.DrainNotDrained,
		RoutingReason:         json.RawMessage(`{"route":"settlement-recovery-integration"}`),
		ImageSizeBreakdown:    json.RawMessage(`{}`),
		StreamProtocolLoss:    nil,
		PendingReconciliation: false,
	}
	if images {
		size := "1024x1024"
		draft.TokensInput = 0
		draft.TokensOutput = 0
		draft.DeliveredTokenCount = 0
		draft.ImageCount = 2
		draft.ImageSize = &size
		draft.ImageSizeBreakdown = json.RawMessage(`{"1024x1024":2}`)
	}
	requestID := fmt.Sprintf("recovery-%d-%s", seed.claimID, uuid.NewString())
	return billing.SettleRequest{
		ClaimID:           seed.claimID,
		AccountID:         seed.providerAccount,
		AcquisitionToken:  seed.acquisitionToken,
		ActualCost:        actualCost,
		TenantID:          seed.tenantID,
		APIKeyID:          seed.apiKeyID,
		UserID:            seed.userID,
		ProviderAccountID: seed.providerAccount,
		AttemptSeq:        1,
		RequestedModel:    "gpt-image-1",
		RequestedAt:       time.Now().UTC(),
		UpstreamModel:     "gpt-image-1",
		Provider:          "openai",
		Stream:            false,
		Draft:             draft,
		Fingerprint:       seed.fingerprint,
		AuditRequestID:    requestID,
		SnapshotVersion:   "registry:test;router:test",
	}
}

func assertRecoveryBalance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed recoveryMoneySeed, wantBalance, wantHeld string) {
	t.Helper()
	var balance, held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID,
	).Scan(&balance, &held); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	wantBalanceDecimal := decimal.RequireFromString(wantBalance)
	wantHeldDecimal := decimal.RequireFromString(wantHeld)
	if !balance.Equal(wantBalanceDecimal) || !held.Equal(wantHeldDecimal) {
		t.Fatalf("balance/held=%s/%s want %s/%s", balance, held, wantBalanceDecimal, wantHeldDecimal)
	}
}

func assertRecoveryEvidenceCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed recoveryMoneySeed, wantUsage, wantEvents int) {
	t.Helper()
	var usageCount, eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_records WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, seed.claimID,
	).Scan(&usageCount); err != nil {
		t.Fatalf("count usage records: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, seed.claimID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count billing events: %v", err)
	}
	if usageCount != wantUsage || eventCount != wantEvents {
		t.Fatalf("usage/billing event counts=%d/%d want %d/%d", usageCount, eventCount, wantUsage, wantEvents)
	}
}
