//go:build integration_pg

package settlementintent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
)

// TestSettlementIntentSweeperPostgresReconciliation 用真实 PostgreSQL 验证权威追平、
// 宽限过滤和多副本并发单胜者。
func TestSettlementIntentSweeperPostgresReconciliation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := db.Open(ctx, db.PoolConfig{DSN: settlementIntentTestDSN(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	t.Run("committed_权威金额追平", func(t *testing.T) {
		now := time.Now().UTC()
		fixture := seedSweepFixture(t, ctx, pool, 1)
		store := NewPostgresStore(dbbilling.New(pool))
		version, err := store.MarkDelivering(ctx, fixture.intentID, 0, now.Add(-20*time.Minute))
		if err != nil || version != 1 {
			t.Fatalf("MarkDelivering version=%d err=%v", version, err)
		}
		makeIntentOld(t, ctx, pool, fixture.intentID, now.Add(-20*time.Minute))
		cost := decimal.RequireFromString("1.23456789")
		commitClaim(t, ctx, pool, fixture, cost)

		result, err := runPostgresSweep(ctx, pool, now, SweeperOptions{})
		if err != nil || result.Settled != 1 {
			t.Fatalf("RunOnce result=%+v err=%v", result, err)
		}
		row := loadIntent(t, ctx, pool, fixture, 1)
		if row.Status != "settled" || row.Version != 2 || !row.ActualCost.Equal(cost) || !row.SettledAt.Valid {
			t.Fatalf("committed 追平结果=%+v", row)
		}
	})

	t.Run("aborted_追平且不写金额", func(t *testing.T) {
		now := time.Now().UTC()
		fixture := seedSweepFixture(t, ctx, pool, 1)
		makeIntentOld(t, ctx, pool, fixture.intentID, now.Add(-20*time.Minute))
		abortClaim(t, ctx, pool, fixture)

		result, err := runPostgresSweep(ctx, pool, now, SweeperOptions{})
		if err != nil || result.Aborted != 1 {
			t.Fatalf("RunOnce result=%+v err=%v", result, err)
		}
		row := loadIntent(t, ctx, pool, fixture, 1)
		if row.Status != "aborted" || row.Version != 1 || !row.ActualCost.IsZero() || row.SettledAt.Valid {
			t.Fatalf("aborted 追平结果=%+v", row)
		}
	})

	t.Run("更高_attempt_只能_superseded", func(t *testing.T) {
		now := time.Now().UTC()
		fixture := seedSweepFixture(t, ctx, pool, 1)
		makeIntentOld(t, ctx, pool, fixture.intentID, now.Add(-20*time.Minute))
		abortClaim(t, ctx, pool, fixture)
		queries := dbbilling.New(pool)
		revived, err := queries.ReReserveAbortedClaim(ctx, dbbilling.ReReserveAbortedClaimParams{
			ID:             fixture.claimID,
			LeaseExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
			PredictedCost:  decimal.NewFromInt(2),
			TenantID:       fixture.tenantID,
		})
		if err != nil || revived.AttemptSeq != 2 {
			t.Fatalf("ReReserveAbortedClaim=%+v err=%v", revived, err)
		}
		newAttemptCost := decimal.RequireFromString("9.87654321")
		commitClaim(t, ctx, pool, fixture, newAttemptCost)

		result, err := runPostgresSweep(ctx, pool, now, SweeperOptions{})
		if err != nil || result.Superseded != 1 || result.Settled != 0 {
			t.Fatalf("RunOnce result=%+v err=%v", result, err)
		}
		row := loadIntent(t, ctx, pool, fixture, 1)
		// 变异证：删除 attempt_seq 一致性判断后会变成 settled 并复制新 attempt 金额。
		if row.Status != "superseded" || row.Version != 1 || !row.ActualCost.IsZero() || row.SettledAt.Valid {
			t.Fatalf("复活旧意图结果=%+v", row)
		}
	})

	t.Run("reserving_在途保持不变", func(t *testing.T) {
		now := time.Now().UTC()
		fixture := seedSweepFixture(t, ctx, pool, 1)
		store := NewPostgresStore(dbbilling.New(pool))
		version, err := store.MarkDelivering(ctx, fixture.intentID, 0, now.Add(-20*time.Minute))
		if err != nil || version != 1 {
			t.Fatalf("MarkDelivering version=%d err=%v", version, err)
		}
		makeIntentOld(t, ctx, pool, fixture.intentID, now.Add(-20*time.Minute))

		result, err := runPostgresSweep(ctx, pool, now, SweeperOptions{})
		if err != nil || result.Skipped != 1 || result.changed() != 0 {
			t.Fatalf("RunOnce result=%+v err=%v", result, err)
		}
		row := loadIntent(t, ctx, pool, fixture, 1)
		if row.Status != "delivering" || row.Version != 1 || !row.ActualCost.IsZero() {
			t.Fatalf("在途意图被误改=%+v", row)
		}
	})

	t.Run("failed_按权威终态追平并清除恢复载荷", func(t *testing.T) {
		tests := []struct {
			name       string
			arrange    func(t *testing.T, fixture sweepFixture)
			wantStatus string
			wantField  func(SweepResult) int
		}{
			{
				name: "committed",
				arrange: func(t *testing.T, fixture sweepFixture) {
					commitClaim(t, ctx, pool, fixture, decimal.RequireFromString("0.75"))
				},
				wantStatus: "settled",
				wantField:  func(result SweepResult) int { return result.Settled },
			},
			{
				name: "aborted",
				arrange: func(t *testing.T, fixture sweepFixture) {
					abortClaim(t, ctx, pool, fixture)
				},
				wantStatus: "aborted",
				wantField:  func(result SweepResult) int { return result.Aborted },
			},
			{
				name: "new_attempt",
				arrange: func(t *testing.T, fixture sweepFixture) {
					abortClaim(t, ctx, pool, fixture)
					if _, err := dbbilling.New(pool).ReReserveAbortedClaim(ctx, dbbilling.ReReserveAbortedClaimParams{
						ID:             fixture.claimID,
						LeaseExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(time.Hour), Valid: true},
						PredictedCost:  decimal.NewFromInt(2),
						TenantID:       fixture.tenantID,
					}); err != nil {
						t.Fatalf("ReReserveAbortedClaim: %v", err)
					}
				},
				wantStatus: "superseded",
				wantField:  func(result SweepResult) int { return result.Superseded },
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				now := time.Now().UTC()
				fixture := seedSweepFixture(t, ctx, pool, 1)
				store := NewPostgresStore(dbbilling.New(pool))
				payload := json.RawMessage(`{"source":"stream","settle":{"claim_id":1,"tenant_id":1},"request_id":"fixture"}`)
				version, err := store.MarkRecoveryPending(
					ctx,
					fixture.intentID,
					0,
					decimal.RequireFromString("0.75"),
					payload,
					"database_unavailable",
				)
				if err != nil || version != 1 {
					t.Fatalf("MarkRecoveryPending version=%d err=%v", version, err)
				}
				makeIntentOld(t, ctx, pool, fixture.intentID, now.Add(-20*time.Minute))
				tc.arrange(t, fixture)

				result, err := runPostgresSweep(ctx, pool, now, SweeperOptions{})
				if err != nil || tc.wantField(result) != 1 {
					t.Fatalf("RunOnce result=%+v err=%v", result, err)
				}
				row := loadIntent(t, ctx, pool, fixture, 1)
				if row.Status != tc.wantStatus ||
					len(row.RecoveryPayload) != 0 ||
					row.RecoveryFailureClass != nil {
					t.Fatalf("权威终态追平结果=%+v", row)
				}
			})
		}
	})

	t.Run("failed_多副本重投只留一条正式恢复记录", func(t *testing.T) {
		now := time.Now().UTC()
		fixture := seedSweepFixture(t, ctx, pool, 1)
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			_, _ = pool.Exec(cleanupCtx, `
				DELETE FROM usage_record_dlq
				WHERE tenant_id=$1 AND event_kind='post_delivery_settlement' AND claim_id=$2`,
				fixture.tenantID,
				fixture.claimID,
			)
		})
		recoveryPayload := settlementrecovery.FromSettleRequest(
			settlementrecovery.SourceStream,
			"request-requeue-"+uuid.NewString(),
			billing.SettleRequest{
				ClaimID:    fixture.claimID,
				TenantID:   fixture.tenantID,
				ActualCost: decimal.RequireFromString("0.75"),
			},
		)
		raw, err := recoveryPayload.Encode()
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		store := NewPostgresStore(dbbilling.New(pool))
		version, err := store.MarkRecoveryPending(
			ctx,
			fixture.intentID,
			0,
			decimal.RequireFromString("0.75"),
			raw,
			"database_unavailable",
		)
		if err != nil || version != 1 {
			t.Fatalf("MarkRecoveryPending version=%d err=%v", version, err)
		}
		makeIntentOld(t, ctx, pool, fixture.intentID, now.Add(-20*time.Minute))

		queue := legacydlq.NewStore(pool)
		publisher := RecoveryPublisherFunc(func(
			publishCtx context.Context,
			encoded json.RawMessage,
			failureClass string,
		) error {
			payload, decodeErr := settlementrecovery.Decode(encoded)
			if decodeErr != nil {
				return decodeErr
			}
			_, enqueueErr := settlementrecovery.EnqueuePayload(publishCtx, queue, payload, failureClass)
			return enqueueErr
		})

		const workers = 8
		start := make(chan struct{})
		var wg sync.WaitGroup
		var requeued atomic.Int32
		errCh := make(chan error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				result, runErr := runPostgresSweep(ctx, pool, now, SweeperOptions{Recovery: publisher})
				if runErr != nil {
					errCh <- runErr
					return
				}
				requeued.Add(int32(result.Requeued))
			}()
		}
		close(start)
		wg.Wait()
		close(errCh)
		for runErr := range errCh {
			t.Errorf("并发重投错误: %v", runErr)
		}
		if requeued.Load() != 1 {
			t.Fatalf("重投 CAS 胜者=%d want 1", requeued.Load())
		}
		row := loadIntent(t, ctx, pool, fixture, 1)
		if row.Status != "settling" || row.Version != 2 || len(row.RecoveryPayload) == 0 {
			t.Fatalf("重投后意图=%+v", row)
		}
		var queueRows int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM usage_record_dlq
			WHERE tenant_id=$1 AND event_kind='post_delivery_settlement' AND claim_id=$2`,
			fixture.tenantID,
			fixture.claimID,
		).Scan(&queueRows); err != nil {
			t.Fatalf("count recovery queue: %v", err)
		}
		if queueRows != 1 {
			t.Fatalf("正式恢复记录=%d want 1", queueRows)
		}
	})

	t.Run("多副本与正向_hook_并发单胜者", func(t *testing.T) {
		now := time.Now().UTC()
		fixture := seedSweepFixture(t, ctx, pool, 1)
		queries := dbbilling.New(pool)
		store := NewPostgresStore(queries)
		version, err := store.MarkDelivering(ctx, fixture.intentID, 0, now.Add(-20*time.Minute))
		if err != nil || version != 1 {
			t.Fatalf("MarkDelivering version=%d err=%v", version, err)
		}
		makeIntentOld(t, ctx, pool, fixture.intentID, now.Add(-20*time.Minute))
		cost := decimal.RequireFromString("3.21000000")
		commitClaim(t, ctx, pool, fixture, cost)
		workerA := newPostgresSweeper(pool, SweeperOptions{})
		workerB := newPostgresSweeper(pool, SweeperOptions{})

		const goroutines = 12
		start := make(chan struct{})
		var wg sync.WaitGroup
		var successes atomic.Int32
		errCh := make(chan error, goroutines)
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(kind int) {
				defer wg.Done()
				<-start
				switch kind % 3 {
				case 0:
					result, runErr := workerA.RunOnce(ctx, now)
					if runErr != nil {
						errCh <- runErr
						return
					}
					successes.Add(int32(result.Settled))
				case 1:
					_, markErr := store.MarkSettled(ctx, fixture.intentID, 1, cost, now)
					switch {
					case markErr == nil:
						successes.Add(1)
					case errors.Is(markErr, pgx.ErrNoRows):
					default:
						errCh <- markErr
					}
				case 2:
					result, runErr := workerB.RunOnce(ctx, now)
					if runErr != nil {
						errCh <- runErr
						return
					}
					successes.Add(int32(result.Settled))
				}
			}(i)
		}
		close(start)
		wg.Wait()
		close(errCh)
		for goroutineErr := range errCh {
			t.Errorf("并发竞争错误: %v", goroutineErr)
		}
		if got := successes.Load(); got != 1 {
			t.Fatalf("并发成功方=%d want 1", got)
		}
		row := loadIntent(t, ctx, pool, fixture, 1)
		if row.Status != "settled" || row.Version != 2 || !row.ActualCost.Equal(cost) {
			t.Fatalf("并发终态=%+v", row)
		}
		var rowCount int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM settlement_intents WHERE id=$1`, fixture.intentID).Scan(&rowCount); err != nil {
			t.Fatalf("count intent: %v", err)
		}
		if rowCount != 1 {
			t.Fatalf("意图行数=%d want 1", rowCount)
		}

		// 变异证：删除任一 stale 写的 status IN 守卫后，当前 version 的终态会被反向改写。
		// 三条守卫都要各自锁定，避免只测其一而漏掉另两条被误删。
		if _, err := store.MarkAbortedIfStale(ctx, fixture.intentID, row.Version); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("已终态 AbortedIfStale err=%v want pgx.ErrNoRows", err)
		}
		if _, err := store.MarkSettledIfStale(ctx, fixture.intentID, row.Version, cost, row.SettledAt.Time); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("已终态 SettledIfStale err=%v want pgx.ErrNoRows", err)
		}
		if _, err := store.MarkSupersededIfStale(ctx, fixture.intentID, row.Version); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("已终态 SupersededIfStale err=%v want pgx.ErrNoRows", err)
		}
		afterGuard := loadIntent(t, ctx, pool, fixture, 1)
		if afterGuard.Status != "settled" || afterGuard.Version != 2 {
			t.Fatalf("终态守卫后意图=%+v", afterGuard)
		}
	})

	t.Run("created_at_宽限排除新意图", func(t *testing.T) {
		now := time.Now().UTC()
		fixture := seedSweepFixture(t, ctx, pool, 1)
		abortClaim(t, ctx, pool, fixture)
		insideGrace := now.Add(-5 * time.Second)
		if _, err := pool.Exec(ctx, `UPDATE settlement_intents SET created_at=$2, updated_at=$2 WHERE id=$1`, fixture.intentID, insideGrace); err != nil {
			t.Fatalf("设置宽限夹具: %v", err)
		}

		result, err := runPostgresSweep(ctx, pool, now, SweeperOptions{
			StaleAfter: time.Second, CreatedGrace: 10 * time.Second,
		})
		if err != nil || result.Scanned != 0 {
			t.Fatalf("RunOnce result=%+v err=%v", result, err)
		}
		row := loadIntent(t, ctx, pool, fixture, 1)
		// 变异证：删除 created_at 宽限条件后，该行满足 updated_at 陈旧条件并会变 aborted。
		if row.Status != "pending" || row.Version != 0 {
			t.Fatalf("宽限内意图被扫描=%+v", row)
		}
	})

	t.Run("阶段1_正向_Mark_保持原语义", func(t *testing.T) {
		now := time.Now().UTC()
		fixture := seedSweepFixture(t, ctx, pool, 1)
		store := NewPostgresStore(dbbilling.New(pool))
		version, err := store.MarkDelivering(ctx, fixture.intentID, 0, now)
		if err != nil || version != 1 {
			t.Fatalf("MarkDelivering version=%d err=%v", version, err)
		}
		cost := decimal.RequireFromString("0.76543210")
		version, err = store.MarkSettling(ctx, fixture.intentID, version, cost)
		if err != nil || version != 2 {
			t.Fatalf("MarkSettling version=%d err=%v", version, err)
		}
		version, err = store.MarkSettled(ctx, fixture.intentID, version, cost, now)
		if err != nil || version != 3 {
			t.Fatalf("MarkSettled version=%d err=%v", version, err)
		}
		row := loadIntent(t, ctx, pool, fixture, 1)
		if row.Status != "settled" || row.Version != 3 || !row.ActualCost.Equal(cost) || !row.FirstByteAt.Valid || !row.SettledAt.Valid {
			t.Fatalf("阶段 1 正向生命周期=%+v", row)
		}
	})
}

type sweepFixture struct {
	tenantID int64
	userID   int64
	apiKeyID int64
	claimID  int64
	intentID int64
}

func seedSweepFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, attemptSeq int32) sweepFixture {
	t.Helper()
	suffix := uuid.NewString()
	fixture := sweepFixture{}
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "sweep-"+suffix).Scan(&fixture.tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (tenant_id, email, display_name)
		VALUES ($1, $2, $3)
		RETURNING id`, fixture.tenantID, suffix+"@example.test", "结算意图对账").Scan(&fixture.userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		VALUES ($1, $2, $3, $4, $5, 'active')
		RETURNING id`, fixture.tenantID, fixture.userID, "sweep-"+suffix, "hash-"+suffix, "si-"+suffix[:8]).Scan(&fixture.apiKeyID); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, billing_policy_version,
			request_class, attempt_seq, predicted_cost, currency_code, status, lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'chat', 'gpt-4o', 'test-policy',
			'standard', $7, 1.00000000, 'USD', 'reserving', $8)
		RETURNING id`, fixture.tenantID, "idem-"+suffix, "claim-fingerprint-"+suffix,
		fixture.apiKeyID, fixture.userID, "logical-"+suffix, attemptSeq, time.Now().UTC().Add(time.Hour)).Scan(&fixture.claimID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	store := NewPostgresStore(dbbilling.New(pool))
	intentID, err := store.Insert(ctx, CreateParams{
		TenantID:           fixture.tenantID,
		RequestID:          "request-" + suffix,
		LogicalRequestID:   "logical-" + suffix,
		AttemptSeq:         attemptSeq,
		ClaimID:            fixture.claimID,
		APIKeyID:           fixture.apiKeyID,
		RequestFingerprint: "intent-fingerprint-" + suffix,
		PredictedCost:      decimal.NewFromInt(1),
	})
	if err != nil {
		t.Fatalf("insert intent: %v", err)
	}
	fixture.intentID = intentID
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM settlement_intents WHERE tenant_id=$1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE tenant_id=$1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, fixture.tenantID)
	})
	return fixture
}

func makeIntentOld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, intentID int64, timestamp time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE settlement_intents SET created_at=$2, updated_at=$2 WHERE id=$1`, intentID, timestamp); err != nil {
		t.Fatalf("设置陈旧意图: %v", err)
	}
}

func commitClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture sweepFixture, cost decimal.Decimal) {
	t.Helper()
	rows, err := dbbilling.New(pool).UpdateClaimCommitted(ctx, dbbilling.UpdateClaimCommittedParams{
		ID: fixture.claimID, ActualCost: decimal.NullDecimal{Decimal: cost, Valid: true}, TenantID: fixture.tenantID,
	})
	if err != nil || rows != 1 {
		t.Fatalf("UpdateClaimCommitted rows=%d err=%v", rows, err)
	}
}

func abortClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture sweepFixture) {
	t.Helper()
	reason := "integration_test"
	rows, err := dbbilling.New(pool).AbortClaim(ctx, dbbilling.AbortClaimParams{
		ID: fixture.claimID, AbortedReason: &reason, TenantID: fixture.tenantID,
	})
	if err != nil || rows != 1 {
		t.Fatalf("AbortClaim rows=%d err=%v", rows, err)
	}
}

func runPostgresSweep(ctx context.Context, pool *pgxpool.Pool, now time.Time, opts SweeperOptions) (SweepResult, error) {
	return newPostgresSweeper(pool, opts).RunOnce(ctx, now)
}

func newPostgresSweeper(pool *pgxpool.Pool, opts SweeperOptions) *SettlementIntentSweeper {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	queries := dbbilling.New(pool)
	return NewSettlementIntentSweeper(NewPostgresStore(queries), NewPostgresClaimAuthority(queries), opts)
}

func loadIntent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture sweepFixture, attemptSeq int32) dbbilling.SettlementIntent {
	t.Helper()
	row, err := dbbilling.New(pool).GetSettlementIntentByClaimAttempt(ctx, dbbilling.GetSettlementIntentByClaimAttemptParams{
		TenantID: fixture.tenantID, ClaimID: fixture.claimID, AttemptSeq: attemptSeq,
	})
	if err != nil {
		t.Fatalf("GetSettlementIntentByClaimAttempt: %v", err)
	}
	return row
}

func settlementIntentTestDSN(t *testing.T) string {
	t.Helper()
	if value := os.Getenv("HUAKAI_TEST_DATABASE_URL"); value != "" {
		return value
	}
	if value := os.Getenv("HUAKAI_DATABASE_URL"); value != "" {
		return value
	}
	t.Fatal("integration_pg 必须设置 HUAKAI_TEST_DATABASE_URL 或 HUAKAI_DATABASE_URL")
	return ""
}
