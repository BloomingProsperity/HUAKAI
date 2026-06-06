//go:build integration_pg

package billing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	db "github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestBalanceHold_ConcurrentOverspendDiscriminating(t *testing.T) {
	// Mutation check: remove `(balance - held) >= @cost` from ReserveBalanceHold.
	// Without guard, all 5 goroutines succeed and held becomes 15.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openTestPool(t, ctx)
	tenantID, userID, apiKeyID := seedTenantAndBalanceUser(t, ctx, pool)

	claims := make([]int64, 5)
	for i := 0; i < 5; i++ {
		if err := pool.QueryRow(ctx,
			`INSERT INTO billing_ledger_claims (tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id, logical_request_id, endpoint_family, requested_model, billing_policy_version, request_class, attempt_seq, predicted_cost, currency_code, lease_expires_at)
			VALUES ($1, $2, $3, $4, $5, $3, 'chat', 'gpt-4', '1.0', 'standard', 1, 0.01, 'USD', NOW() + interval '90 seconds') RETURNING id`,
			tenantID, fmt.Sprintf("idem-%d-%d", i, userID), fmt.Sprintf("fp-%d", i), apiKeyID, userID,
		).Scan(&claims[i]); err != nil {
			t.Fatalf("seed claim %d: %v", i, err)
		}
	}

	var ok, insufficient int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap, err := reserveWithRetry(ctx, pool, tenantID, userID, claims[i], decimal.NewFromInt(3), 10)
			if err != nil {
				if errors.Is(err, ErrBalanceHoldInsufficientBalance) {
					mu.Lock()
					insufficient++
					mu.Unlock()
					return
				}
				t.Errorf("reserve %d: %v", i, err)
				return
			}
			_ = snap
			mu.Lock()
			ok++
			mu.Unlock()
		}()
	}
	wg.Wait()

	if ok != 3 || insufficient != 2 {
		t.Fatalf("overspend gate: ok=%d insufficient=%d want 3/2", ok, insufficient)
	}

	var balance, held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID,
	).Scan(&balance, &held); err != nil {
		t.Fatalf("read user balance: %v", err)
	}
	if !balance.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("balance=%s want 10", balance)
	}
	if !held.Equal(decimal.NewFromInt(9)) {
		t.Fatalf("held=%s want 9", held)
	}
}

func TestBalanceHold_CaptureChargesActualAndIdempotent(t *testing.T) {
	// Mutation check: use predicted amount in ApplyBalanceHoldCapture instead of actual.
	// With mutation, final balance becomes 5 for predicted=5/actual=3.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openTestPool(t, ctx)
	tenantID, userID, _, claimID := seedTenantBalanceAndClaim(t, ctx, pool, decimal.NewFromInt(10))

	if _, err := execReserve(ctx, pool, tenantID, userID, claimID, decimal.NewFromInt(5)); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := execCapture(ctx, pool, claimID, decimal.NewFromInt(3)); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := execCapture(ctx, pool, claimID, decimal.NewFromInt(3)); err != nil {
		t.Fatalf("capture idempotent: %v", err)
	}

	var balance, held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID,
	).Scan(&balance, &held); err != nil {
		t.Fatalf("read user balance: %v", err)
	}
	if !balance.Equal(decimal.NewFromInt(7)) || !held.Equal(decimal.Zero) {
		t.Fatalf("after capture: balance=%s held=%s want 7/0", balance, held)
	}
	var state string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM balance_holds WHERE claim_id=$1`,
		claimID,
	).Scan(&state); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state != "captured" {
		t.Fatalf("hold state=%s want captured", state)
	}
}

func TestBalanceHold_ReleaseStateIdempotent(t *testing.T) {
	// Mutation check: remove state guard in ApplyBalanceHoldRelease.
	// Without guard, second release makes held negative.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openTestPool(t, ctx)
	tenantID, userID, _, claimID := seedTenantBalanceAndClaim(t, ctx, pool, decimal.NewFromInt(10))

	if _, err := execReserve(ctx, pool, tenantID, userID, claimID, decimal.NewFromInt(5)); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := execRelease(ctx, pool, claimID); err != nil {
		t.Fatalf("release1: %v", err)
	}
	if err := execRelease(ctx, pool, claimID); err != nil {
		t.Fatalf("release2: %v", err)
	}

	var held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID,
	).Scan(&held); err != nil {
		t.Fatalf("read held: %v", err)
	}
	if !held.Equal(decimal.Zero) {
		t.Fatalf("held=%s want 0", held)
	}

	var state string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM balance_holds WHERE claim_id=$1`,
		claimID,
	).Scan(&state); err != nil {
		t.Fatalf("read hold state: %v", err)
	}
	if state != "released" {
		t.Fatalf("hold state=%s want released", state)
	}
}

func TestBalanceHold_MissingRowAllowsOptIn(t *testing.T) {
	// opt-in 余额强制:无 user_balances 行 = 用户未 provision
	// = 不纳入余额强制 → Reserve 放行(返 nil)且不建 hold 行。
	// Mutation check: 若把无行当 ErrBalanceHoldInsufficientBalance(旧 D4 严格语义),Reserve 返错
	// → 本测试在 "expected allow" 处变红;或若误建 hold 行,count!=0 变红。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openTestPool(t, ctx)
	tenantID, userID, apiKeyID := seedTenantNoBalance(t, ctx, pool)
	var claimID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id, logical_request_id, endpoint_family, requested_model, billing_policy_version, request_class, attempt_seq, predicted_cost, currency_code, lease_expires_at)
		 VALUES ($1, 'optin-no-balance', 'fp', $2, $3, 'lr', 'chat', 'gpt-4', '1.0', 'standard', 1, 0.01, 'USD', NOW() + interval '90 seconds')
		 RETURNING id`,
		tenantID, apiKeyID, userID,
	).Scan(&claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 无余额行 → 放行(nil),不是 ErrBalanceHoldInsufficientBalance。
	if _, err := Reserve(ctx, tx, ReserveParams{
		TenantID: tenantID, UserID: userID, ClaimID: claimID, Cost: decimal.NewFromInt(1),
		EnforcementMode: EnforcementModeOptIn,
	}); err != nil {
		t.Fatalf("expected allow (nil) for opt-in user with no balance row, got %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// opt-in 放行不应建 hold 行(无余额可占)。
	var holdRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM balance_holds WHERE claim_id=$1`, claimID).Scan(&holdRows); err != nil {
		t.Fatalf("count holds: %v", err)
	}
	if holdRows != 0 {
		t.Fatalf("expected no hold row for opt-in-allowed user, got %d", holdRows)
	}
}

func TestBalanceHold_MissingRowMandatoryRejects(t *testing.T) {
	// Mutation check: treating mandatory the same as opt-in lets a no-balance
	// user through and fails both the ErrBalanceHoldInsufficientBalance assertion and the
	// "no hold row" guard.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openTestPool(t, ctx)
	tenantID, userID, apiKeyID := seedTenantNoBalance(t, ctx, pool)
	var claimID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id, logical_request_id, endpoint_family, requested_model, billing_policy_version, request_class, attempt_seq, predicted_cost, currency_code, lease_expires_at)
		 VALUES ($1, 'mandatory-no-balance', 'fp-mandatory', $2, $3, 'lr-mandatory', 'chat', 'gpt-4', '1.0', 'standard', 1, 0.01, 'USD', NOW() + interval '90 seconds')
		 RETURNING id`,
		tenantID, apiKeyID, userID,
	).Scan(&claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = Reserve(ctx, tx, ReserveParams{
		TenantID: tenantID, UserID: userID, ClaimID: claimID, Cost: decimal.NewFromInt(1),
		EnforcementMode: EnforcementModeMandatory,
	})
	if !errors.Is(err, ErrBalanceHoldInsufficientBalance) {
		t.Fatalf("mandatory missing balance row err=%v want %v", err, ErrBalanceHoldInsufficientBalance)
	}

	var holdRows int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM balance_holds WHERE claim_id=$1`, claimID).Scan(&holdRows); err != nil {
		t.Fatalf("count holds: %v", err)
	}
	if holdRows != 0 {
		t.Fatalf("mandatory rejection must not create hold row, got %d", holdRows)
	}
}

func TestBalanceHold_BackfillHistoricalVoucherUserCanReserveMandatory(t *testing.T) {
	// Mutation check: deleting the 0065 voucher_redemption backfill statement,
	// filtering out USD redemptions, or forgetting cents->USD conversion leaves
	// no usable $100 balance and Reserve returns ErrBalanceHoldInsufficientBalance.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openTestPool(t, ctx)
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantID, userID, apiKeyID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		fmt.Sprintf("tenant-bh-backfill-%d", time.Now().UTC().UnixNano()),
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, 'historical-user') RETURNING id`,
		tenantID,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, 'k', 'ph', 'hk', 'active') RETURNING id`,
		tenantID, userID,
	).Scan(&apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	var voucherID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO voucher (tenant_id, code_hash, code_fingerprint, amount_cents, currency_code, valid_from, valid_until)
		 VALUES ($1, decode('01', 'hex'), 'old-voucher', 10000, 'USD', NOW() - interval '1 hour', NOW() + interval '1 hour')
		 RETURNING id`,
		tenantID,
	).Scan(&voucherID); err != nil {
		t.Fatalf("seed voucher: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO voucher_redemption (tenant_id, voucher_id, user_id, idempotency_key, amount_cents, currency_code)
		 VALUES ($1, $2, $3, 'old-redeem', 10000, 'USD')`,
		tenantID, voucherID, userID,
	); err != nil {
		t.Fatalf("seed voucher redemption: %v", err)
	}
	var claimID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id, logical_request_id, endpoint_family, requested_model, billing_policy_version, request_class, attempt_seq, predicted_cost, currency_code, lease_expires_at)
		 VALUES ($1, 'backfill-mandatory', 'fp-backfill', $2, $3, 'lr-backfill', 'chat', 'gpt-4', '1.0', 'standard', 1, 100, 'USD', NOW() + interval '90 seconds')
		 RETURNING id`,
		tenantID, apiKeyID, userID,
	).Scan(&claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	backfillSQL := readMarkedMigrationSQL(t,
		"../../sql/migrations/0065_backfill_user_balances.up.sql",
		"-- balance backfill begin",
		"-- balance backfill end",
	)
	if _, err := tx.Exec(ctx, backfillSQL); err != nil {
		t.Fatalf("run marked backfill SQL: %v", err)
	}

	snap, err := Reserve(ctx, tx, ReserveParams{
		TenantID: tenantID, UserID: userID, ClaimID: claimID, Cost: decimal.NewFromInt(100),
		EnforcementMode: EnforcementModeMandatory,
	})
	if err != nil {
		t.Fatalf("reserve after historical voucher backfill: %v", err)
	}
	if !snap.Balance.Equal(decimal.NewFromInt(100)) || !snap.Held.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("snapshot balance/held=%s/%s want 100/100", snap.Balance, snap.Held)
	}
}

func TestBalanceHold_BackfillReconcilesExistingWalletRows(t *testing.T) {
	// Mutation check: keeping a NOT EXISTS(user_balances) filter in the
	// backfill leaves this user at the existing $50 recharge row, so a $150
	// mandatory reserve fails even though the durable ledger says $100 voucher
	// credit + $50 recharge credit are spendable.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openTestPool(t, ctx)
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantID, userID, apiKeyID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		fmt.Sprintf("tenant-bh-backfill-existing-%d", time.Now().UTC().UnixNano()),
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, 'historical-existing-user') RETURNING id`,
		tenantID,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, 'k', 'ph', 'hk', 'active') RETURNING id`,
		tenantID, userID,
	).Scan(&apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	var voucherID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO voucher (tenant_id, code_hash, code_fingerprint, amount_cents, currency_code, valid_from, valid_until)
		 VALUES ($1, decode('02', 'hex'), 'old-voucher-existing', 10000, 'USD', NOW() - interval '1 hour', NOW() + interval '1 hour')
		 RETURNING id`,
		tenantID,
	).Scan(&voucherID); err != nil {
		t.Fatalf("seed voucher: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO voucher_redemption (tenant_id, voucher_id, user_id, idempotency_key, amount_cents, currency_code)
		 VALUES ($1, $2, $3, 'old-redeem-existing', 10000, 'USD')`,
		tenantID, voucherID, userID,
	); err != nil {
		t.Fatalf("seed voucher redemption: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 50, 0)`,
		tenantID, userID,
	); err != nil {
		t.Fatalf("seed existing recharge balance: %v", err)
	}
	var rechargeOrderID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO recharge_orders (tenant_id, user_id, external_trade_no, recharge_ref, status, requested_amount, credited_amount, currency_code, provider, completed_at)
		 VALUES ($1, $2, 'trade-backfill-existing', 'recharge-backfill-existing', 'COMPLETED', 50, 50, 'USD', 'manual', NOW())
		 RETURNING id`,
		tenantID, userID,
	).Scan(&rechargeOrderID); err != nil {
		t.Fatalf("seed recharge order: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO billing_events (tenant_id, event_type, actual_cost, actual_cost_signed, stream_state, delivered_token_count, fingerprint, recharge_order_id)
		 VALUES ($1, 'balance_recharged', 50, 50, 2, 0, 'recharge-backfill-existing', $2)`,
		tenantID, rechargeOrderID,
	); err != nil {
		t.Fatalf("seed recharge billing event: %v", err)
	}
	var claimID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id, logical_request_id, endpoint_family, requested_model, billing_policy_version, request_class, attempt_seq, predicted_cost, currency_code, lease_expires_at)
		 VALUES ($1, 'backfill-existing-mandatory', 'fp-backfill-existing', $2, $3, 'lr-backfill-existing', 'chat', 'gpt-4', '1.0', 'standard', 1, 150, 'USD', NOW() + interval '90 seconds')
		 RETURNING id`,
		tenantID, apiKeyID, userID,
	).Scan(&claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	backfillSQL := readMarkedMigrationSQL(t,
		"../../sql/migrations/0065_backfill_user_balances.up.sql",
		"-- balance backfill begin",
		"-- balance backfill end",
	)
	if _, err := tx.Exec(ctx, backfillSQL); err != nil {
		t.Fatalf("run marked backfill SQL: %v", err)
	}

	snap, err := Reserve(ctx, tx, ReserveParams{
		TenantID: tenantID, UserID: userID, ClaimID: claimID, Cost: decimal.NewFromInt(150),
		EnforcementMode: EnforcementModeMandatory,
	})
	if err != nil {
		t.Fatalf("reserve after existing wallet backfill: %v", err)
	}
	if !snap.Balance.Equal(decimal.NewFromInt(150)) || !snap.Held.Equal(decimal.NewFromInt(150)) {
		t.Fatalf("snapshot balance/held=%s/%s want 150/150", snap.Balance, snap.Held)
	}
}

func execReserve(ctx context.Context, pool *pgxpool.Pool, tenantID, userID, claimID int64, cost decimal.Decimal) (Snapshot, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	snap, err := Reserve(ctx, tx, ReserveParams{
		TenantID: tenantID,
		UserID:   userID,
		ClaimID:  claimID,
		Cost:     cost,
	})
	if err != nil {
		return snap, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

func readMarkedMigrationSQL(t *testing.T, path, beginMarker, endMarker string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", path, err)
	}
	text := string(raw)
	begin := strings.Index(text, beginMarker)
	end := strings.Index(text, endMarker)
	if begin < 0 || end < 0 || end <= begin {
		t.Fatalf("migration %s missing ordered markers %q / %q", path, beginMarker, endMarker)
	}
	stmt := strings.TrimSpace(text[begin+len(beginMarker) : end])
	if stmt == "" || !strings.Contains(stmt, "voucher_redemption") || !strings.Contains(stmt, "user_balances") {
		t.Fatalf("marked backfill SQL does not connect voucher_redemption to user_balances: %q", stmt)
	}
	return stmt
}

func execCapture(ctx context.Context, pool *pgxpool.Pool, claimID int64, actual decimal.Decimal) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := Capture(ctx, tx, claimID, actual); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func execRelease(ctx context.Context, pool *pgxpool.Pool, claimID int64) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := Release(ctx, tx, claimID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func openTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func reserveWithRetry(ctx context.Context, pool *pgxpool.Pool, tenantID, userID, claimID int64, cost decimal.Decimal, attempts int) (Snapshot, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return Snapshot{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()

		snap, err := Reserve(ctx, tx, ReserveParams{
			TenantID: tenantID,
			UserID:   userID,
			ClaimID:  claimID,
			Cost:     cost,
		})
		if err == nil {
			if err = tx.Commit(ctx); err == nil {
				return snap, nil
			}
		}
		lastErr = err
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
			continue
		}
		return Snapshot{}, err
	}
	if lastErr != nil {
		return Snapshot{}, lastErr
	}
	return Snapshot{}, fmt.Errorf("reserveWithRetry exhausted retries")
}

func seedTenantNoBalance(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, userID, apiKeyID int64) {
	t.Helper()
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		fmt.Sprintf("tenant-bh-no-balance-%d", time.Now().UTC().UnixNano()),
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, 'k', 'ph', 'hk', 'active') RETURNING id`,
		tenantID, userID,
	).Scan(&apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	return tenantID, userID, apiKeyID
}

func seedTenantAndBalanceUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, userID, apiKeyID int64) {
	tenantID, userID, apiKeyID = seedTenantNoBalance(t, ctx, pool)
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0)`,
		tenantID, userID,
	); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	return tenantID, userID, apiKeyID
}

func seedTenantBalanceAndClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, balance decimal.Decimal) (tenantID, userID, apiKeyID, claimID int64) {
	tenantID, userID, apiKeyID = seedTenantNoBalance(t, ctx, pool)
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, $3, 0)`,
		tenantID, userID, balance,
	); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id, logical_request_id, endpoint_family, requested_model, billing_policy_version, request_class, attempt_seq, predicted_cost, currency_code, lease_expires_at)
		 VALUES ($1, 'idem-cache', 'fp', $2, $3, 'lr', 'chat', 'gpt-4', '1.0', 'standard', 1, 0.01, 'USD', NOW() + interval '90 seconds') RETURNING id`,
		tenantID, apiKeyID, userID,
	).Scan(&claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	return tenantID, userID, apiKeyID, claimID
}
