//go:build integration_pg

package db

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// TestSettlementIntentsIdentityConstraintsAndLifecycle 守住 claim attempt 身份、租户外键、
// 非负数据域和乐观锁生命周期，所有夹具都来自同一真实账本关系。
func TestSettlementIntentsIdentityConstraintsAndLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Open(ctx, PoolConfig{DSN: dsn(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	tenantID, apiKeyID, _, claimID := seedSettlementIntentClaim(t, ctx, tx)
	queries := dbbilling.New(tx)
	requestID := "settlement-intent-" + uuid.NewString()
	insert := dbbilling.InsertSettlementIntentParams{
		TenantID:           tenantID,
		RequestID:          requestID,
		AttemptSeq:         1,
		ClaimID:            claimID,
		APIKeyID:           &apiKeyID,
		RequestFingerprint: "fingerprint-1",
		PredictedCost:      decimal.RequireFromString("0.01000000"),
	}
	id, err := queries.InsertSettlementIntent(ctx, insert)
	if err != nil {
		t.Fatalf("InsertSettlementIntent: %v", err)
	}
	if id == 0 {
		t.Fatal("InsertSettlementIntent id=0")
	}

	duplicate := insert
	duplicate.RequestID = "settlement-intent-duplicate-" + uuid.NewString()
	expectSettlementIntentConstraint(t, ctx, tx, "23505", "settlement_intents_claim_attempt_key", func() error {
		_, insertErr := queries.InsertSettlementIntent(ctx, duplicate)
		return insertErr
	})

	missingClaim := insert
	missingClaim.RequestID = "settlement-intent-missing-claim-" + uuid.NewString()
	missingClaim.ClaimID = claimID + 1_000_000
	expectSettlementIntentConstraint(t, ctx, tx, "23503", "settlement_intents_claim_fk", func() error {
		_, insertErr := queries.InsertSettlementIntent(ctx, missingClaim)
		return insertErr
	})

	invalidAttempts := insert
	invalidAttempts.RequestID = "settlement-intent-attempt-zero-" + uuid.NewString()
	invalidAttempts.AttemptSeq = 0
	expectSettlementIntentConstraint(t, ctx, tx, "23514", "settlement_intents_attempt_seq_positive", func() error {
		_, insertErr := queries.InsertSettlementIntent(ctx, invalidAttempts)
		return insertErr
	})

	negativePredicted := insert
	negativePredicted.RequestID = "settlement-intent-negative-predicted-" + uuid.NewString()
	negativePredicted.AttemptSeq = 2
	negativePredicted.PredictedCost = decimal.RequireFromString("-0.00000001")
	expectSettlementIntentConstraint(t, ctx, tx, "23514", "settlement_intents_predicted_cost_nonnegative", func() error {
		_, insertErr := queries.InsertSettlementIntent(ctx, negativePredicted)
		return insertErr
	})

	for _, tc := range []struct {
		name       string
		column     string
		constraint string
	}{
		{name: "actual_cost", column: "actual_cost", constraint: "settlement_intents_actual_cost_nonnegative"},
		{name: "version", column: "version", constraint: "settlement_intents_version_nonnegative"},
		{name: "retry_count", column: "retry_count", constraint: "settlement_intents_retry_count_nonnegative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectSettlementIntentConstraint(t, ctx, tx, "23514", tc.constraint, func() error {
				_, updateErr := tx.Exec(ctx, fmt.Sprintf("UPDATE settlement_intents SET %s = -1 WHERE id = $1", tc.column), id)
				return updateErr
			})
		})
	}

	firstByteAt := time.Now().UTC()
	version, err := queries.MarkSettlementIntentDelivering(ctx, dbbilling.MarkSettlementIntentDeliveringParams{
		ID:          id,
		Version:     0,
		FirstByteAt: pgtype.Timestamptz{Time: firstByteAt, Valid: true},
	})
	if err != nil {
		t.Fatalf("MarkSettlementIntentDelivering: %v", err)
	}
	if version != 1 {
		t.Fatalf("delivering version=%d want 1", version)
	}
	if _, err := queries.MarkSettlementIntentSettling(ctx, dbbilling.MarkSettlementIntentSettlingParams{
		ID: id, Version: 0, ActualCost: decimal.RequireFromString("0.00800000"),
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("陈旧 version 必须被乐观锁拒绝: %v", err)
	}

	actualCost := decimal.RequireFromString("0.00800000")
	version, err = queries.MarkSettlementIntentSettling(ctx, dbbilling.MarkSettlementIntentSettlingParams{
		ID: id, Version: 1, ActualCost: actualCost,
	})
	if err != nil {
		t.Fatalf("MarkSettlementIntentSettling: %v", err)
	}
	if version != 2 {
		t.Fatalf("settling version=%d want 2", version)
	}
	settlingRow, err := queries.GetSettlementIntentByClaimAttempt(ctx, dbbilling.GetSettlementIntentByClaimAttemptParams{
		TenantID: tenantID, ClaimID: claimID, AttemptSeq: 1,
	})
	if err != nil {
		t.Fatalf("GetSettlementIntentByClaimAttempt settling: %v", err)
	}
	if settlingRow.Status != "settling" || !settlingRow.ActualCost.Equal(actualCost) {
		t.Fatalf("settling row 状态或金额不完整: %+v", settlingRow)
	}
	settledAt := time.Now().UTC()
	version, err = queries.MarkSettlementIntentSettled(ctx, dbbilling.MarkSettlementIntentSettledParams{
		ID: id, Version: 2, ActualCost: actualCost,
		SettledAt: pgtype.Timestamptz{Time: settledAt, Valid: true},
	})
	if err != nil {
		t.Fatalf("MarkSettlementIntentSettled: %v", err)
	}
	if version != 3 {
		t.Fatalf("settled version=%d want 3", version)
	}

	row, err := queries.GetSettlementIntentByClaimAttempt(ctx, dbbilling.GetSettlementIntentByClaimAttemptParams{
		TenantID: tenantID, ClaimID: claimID, AttemptSeq: 1,
	})
	if err != nil {
		t.Fatalf("GetSettlementIntentByClaimAttempt attempt 1: %v", err)
	}
	if row.Status != "settled" || !row.FirstByteAt.Valid || !row.SettledAt.Valid || !row.ActualCost.Equal(actualCost) {
		t.Fatalf("settled row 不完整: %+v", row)
	}

	if _, err := tx.Exec(ctx, `UPDATE billing_ledger_claims SET status = 'aborted' WHERE tenant_id = $1 AND id = $2`, tenantID, claimID); err != nil {
		t.Fatalf("abort claim before re-reserve: %v", err)
	}
	revived, err := queries.ReReserveAbortedClaim(ctx, dbbilling.ReReserveAbortedClaimParams{
		ID:             claimID,
		LeaseExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(time.Hour), Valid: true},
		PredictedCost:  insert.PredictedCost,
		TenantID:       tenantID,
	})
	if err != nil {
		t.Fatalf("ReReserveAbortedClaim: %v", err)
	}
	if revived.ID != claimID || revived.AttemptSeq != 2 {
		t.Fatalf("复活结果 id/attempt=%d/%d want %d/2", revived.ID, revived.AttemptSeq, claimID)
	}

	revivedInsert := insert
	revivedInsert.RequestID = "settlement-intent-revived-" + uuid.NewString()
	revivedInsert.AttemptSeq = revived.AttemptSeq
	revivedInsert.RequestFingerprint = "fingerprint-2"
	revivedID, err := queries.InsertSettlementIntent(ctx, revivedInsert)
	if err != nil {
		t.Fatalf("复活 attempt 插入: %v", err)
	}
	failedCost := decimal.RequireFromString("0.00900000")
	if _, err := queries.MarkSettlementIntentFailed(ctx, dbbilling.MarkSettlementIntentFailedParams{
		ID: revivedID, Version: 0, ActualCost: failedCost,
	}); err != nil {
		t.Fatalf("MarkSettlementIntentFailed: %v", err)
	}
	revivedRow, err := queries.GetSettlementIntentByClaimAttempt(ctx, dbbilling.GetSettlementIntentByClaimAttemptParams{
		TenantID: tenantID, ClaimID: claimID, AttemptSeq: 2,
	})
	if err != nil {
		t.Fatalf("GetSettlementIntentByClaimAttempt attempt 2: %v", err)
	}
	if revivedRow.ID != revivedID || revivedRow.Status != "failed" || !revivedRow.ActualCost.Equal(failedCost) {
		t.Fatalf("复活行状态或金额不完整: %+v", revivedRow)
	}

	var attempts []int32
	rows, err := tx.Query(ctx, `
		SELECT attempt_seq
		FROM settlement_intents
		WHERE tenant_id = $1 AND claim_id = $2
		ORDER BY attempt_seq`, tenantID, claimID)
	if err != nil {
		t.Fatalf("query coexisting attempts: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var attempt int32
		if err := rows.Scan(&attempt); err != nil {
			t.Fatalf("scan attempt: %v", err)
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate attempts: %v", err)
	}
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("同 claim 复活前后行=%v want [1 2]", attempts)
	}

	count, err := queries.CountUnresolvedSettlementIntentsForClaim(ctx, dbbilling.CountUnresolvedSettlementIntentsForClaimParams{
		TenantID: tenantID,
		ClaimID:  claimID,
	})
	if err != nil {
		t.Fatalf("CountUnresolvedSettlementIntentsForClaim: %v", err)
	}
	if count != 1 {
		t.Fatalf("failed attempt unresolved count=%d want 1", count)
	}
}

func seedSettlementIntentClaim(t *testing.T, ctx context.Context, tx pgx.Tx) (int64, int64, int64, int64) {
	t.Helper()
	suffix := uuid.NewString()
	var tenantID, apiKeyID, userID, claimID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "settlement-intent-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (tenant_id, email, display_name)
		VALUES ($1, $2, $3)
		RETURNING id`, tenantID, suffix+"@example.test", "Settlement Intent").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, tenantID, userID, "settlement-intent-"+suffix, "hash-"+suffix, "si-"+suffix[:8]).Scan(&apiKeyID); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, billing_policy_version,
			request_class, predicted_cost, currency_code, status, lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'chat', 'gpt-4o', 'test-policy',
			'standard', 0.01000000, 'USD', 'reserving', $7)
		RETURNING id`, tenantID, "idem-"+suffix, "claim-fingerprint-"+suffix, apiKeyID, userID,
		"logical-"+suffix, time.Now().UTC().Add(time.Hour)).Scan(&claimID); err != nil {
		t.Fatalf("insert billing claim: %v", err)
	}
	return tenantID, apiKeyID, userID, claimID
}

func expectSettlementIntentConstraint(t *testing.T, ctx context.Context, tx pgx.Tx, sqlState, constraint string, operation func() error) {
	t.Helper()
	savepoint := "settlement_intent_constraint"
	if _, err := tx.Exec(ctx, "SAVEPOINT "+savepoint); err != nil {
		t.Fatalf("create savepoint: %v", err)
	}
	err := operation()
	if err == nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint)
		_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint)
		t.Fatalf("约束 %s 未拒绝非法写入", constraint)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != sqlState || pgErr.ConstraintName != constraint {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint)
		_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint)
		t.Fatalf("约束错误=%v want SQLSTATE %s constraint %s", err, sqlState, constraint)
	}
	if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint); rollbackErr != nil {
		t.Fatalf("rollback savepoint: %v", rollbackErr)
	}
	if _, releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint); releaseErr != nil {
		t.Fatalf("release savepoint: %v", releaseErr)
	}
}
