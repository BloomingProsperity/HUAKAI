//go:build integration_pg

package checkin

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestCheckinIdempotent_Concurrent(t *testing.T) {
	// Mutation: removing ON CONFLICT from daily_checkin insert makes this test double-credit or return duplicate-key caller errors.
	ctx := context.Background()
	pool := openCheckinIntegrationPool(t, ctx)
	f := newCheckinFixture(t, ctx, pool)
	svc := newEnabledIntegrationService(pool, 11, time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC))

	const goroutines = 32
	var wg sync.WaitGroup
	barrier := make(chan struct{})
	var mu sync.Mutex
	successes := 0
	already := 0
	var unexpected []error
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			_, err := svc.DoCheckin(ctx, f.tenantID, f.userID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrAlreadyCheckedIn):
				already++
			default:
				unexpected = append(unexpected, err)
			}
		}()
	}
	close(barrier)
	wg.Wait()

	if len(unexpected) != 0 {
		t.Fatalf("unexpected concurrent errors: %v", unexpected[0])
	}
	if successes != 1 || already != goroutines-1 {
		t.Fatalf("successes=%d already=%d want 1/%d", successes, already, goroutines-1)
	}
	assertCheckinMoneyState(t, ctx, pool, f.tenantID, f.userID, "2026-06-06", 1, 11, 1)
}

func TestCheckinReplay_SecondSameDay(t *testing.T) {
	// Mutation: mapping AlreadyCheckedIn to a second payment call makes the balance assertion red.
	ctx := context.Background()
	pool := openCheckinIntegrationPool(t, ctx)
	f := newCheckinFixture(t, ctx, pool)
	svc := newEnabledIntegrationService(pool, 11, time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC))

	first, err := svc.DoCheckin(ctx, f.tenantID, f.userID)
	if err != nil {
		t.Fatalf("first DoCheckin: %v", err)
	}
	if first.NewBalance != 11 || first.RewardCents != 11 {
		t.Fatalf("first result=%+v want reward/new_balance 11", first)
	}
	if _, err := svc.DoCheckin(ctx, f.tenantID, f.userID); !errors.Is(err, ErrAlreadyCheckedIn) {
		t.Fatalf("second DoCheckin err=%v want ErrAlreadyCheckedIn", err)
	}
	assertCheckinMoneyState(t, ctx, pool, f.tenantID, f.userID, "2026-06-06", 1, 11, 1)
}

func TestCheckinCrossDay(t *testing.T) {
	// Mutation: using month instead of UTC date in the idempotency key makes the D+1 check-in red.
	ctx := context.Background()
	pool := openCheckinIntegrationPool(t, ctx)
	f := newCheckinFixture(t, ctx, pool)

	day1 := newEnabledIntegrationService(pool, 11, time.Date(2026, 6, 6, 23, 30, 0, 0, time.UTC))
	if _, err := day1.DoCheckin(ctx, f.tenantID, f.userID); err != nil {
		t.Fatalf("day1 DoCheckin: %v", err)
	}
	day2 := newEnabledIntegrationService(pool, 11, time.Date(2026, 6, 7, 0, 5, 0, 0, time.UTC))
	res, err := day2.DoCheckin(ctx, f.tenantID, f.userID)
	if err != nil {
		t.Fatalf("day2 DoCheckin: %v", err)
	}
	if res.NewBalance != 22 {
		t.Fatalf("day2 NewBalance=%d want 22", res.NewBalance)
	}
	assertCheckinMoneyState(t, ctx, pool, f.tenantID, f.userID, "2026-06-06", 2, 22, 2)
}

func TestCheckinDisabled(t *testing.T) {
	// Mutation: moving the enabled check after ApplyCheckinReward makes the no-row/no-credit assertions red.
	ctx := context.Background()
	pool := openCheckinIntegrationPool(t, ctx)
	f := newCheckinFixture(t, ctx, pool)
	settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	svc := NewService(Deps{
		Store:    NewPostgresStore(pool),
		Payment:  payment.NewService(payment.NewPostgresStore(pool)),
		Settings: settings,
	}, WithClock(func() time.Time {
		return time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	}))

	if _, err := svc.DoCheckin(ctx, f.tenantID, f.userID); !errors.Is(err, ErrDisabled) {
		t.Fatalf("DoCheckin disabled err=%v want ErrDisabled", err)
	}
	assertCheckinMoneyState(t, ctx, pool, f.tenantID, f.userID, "2026-06-06", 0, 0, 0)
}

func newEnabledIntegrationService(pool *pgxpool.Pool, rewardCents int64, now time.Time) *Service {
	settingsStore := platformsettings.NewMemoryStore()
	_, _ = settingsStore.Upsert(context.Background(), platformsettings.GlobalScope, string(platformsettings.KeyCheckinEnabled), "true", "test")
	_, _ = settingsStore.Upsert(context.Background(), platformsettings.GlobalScope, string(platformsettings.KeyCheckinMinCents), intString(rewardCents), "test")
	_, _ = settingsStore.Upsert(context.Background(), platformsettings.GlobalScope, string(platformsettings.KeyCheckinMaxCents), intString(rewardCents), "test")
	return NewService(Deps{
		Store:    NewPostgresStore(pool),
		Payment:  payment.NewService(payment.NewPostgresStore(pool)),
		Settings: platformsettings.NewService(settingsStore, nil),
	}, WithClock(func() time.Time { return now }))
}

func openCheckinIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 40})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type checkinFixture struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	tenantID int64
	userID   int64
}

func newCheckinFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *checkinFixture {
	t.Helper()
	f := &checkinFixture{t: t, ctx: ctx, pool: pool}
	suffix := uuid.NewString()
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "checkin-"+suffix).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name, status) VALUES ($1, $2, 'active') RETURNING id`,
		f.tenantID, "checkin-user-"+suffix).Scan(&f.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(f.cleanup)
	return f
}

func (f *checkinFixture) cleanup() {
	ctx := context.Background()
	_, _ = f.pool.Exec(ctx, `DELETE FROM daily_checkin WHERE tenant_id=$1`, f.tenantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM payment_audit_events WHERE tenant_id=$1`, f.tenantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM billing_events WHERE tenant_id=$1`, f.tenantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM payment_credits WHERE tenant_id=$1`, f.tenantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM payment_orders WHERE tenant_id=$1`, f.tenantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM user_balances WHERE tenant_id=$1`, f.tenantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, f.tenantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, f.tenantID)
}

func assertCheckinMoneyState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, date string, wantRows, wantBalance, wantBilling int64) {
	t.Helper()
	assertCount(t, ctx, pool, `SELECT count(*) FROM daily_checkin WHERE tenant_id=$1 AND user_id=$2 AND checkin_date >= $3::date`, wantRows, tenantID, userID, date)
	assertCount(t, ctx, pool, `
SELECT count(*)
FROM billing_events be
JOIN daily_checkin dc ON dc.billing_event_id = be.id
WHERE dc.tenant_id=$1 AND dc.user_id=$2 AND be.event_type='payment_credited'`, wantBilling, tenantID, userID)
	var balance int64
	if err := pool.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_cents), 0)::bigint
FROM payment_credits
WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID).Scan(&balance); err != nil {
		t.Fatalf("read payment_credits balance: %v", err)
	}
	if balance != wantBalance {
		t.Fatalf("payment_credits balance=%d want %d", balance, wantBalance)
	}
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, want int64, args ...any) {
	t.Helper()
	var got int64
	if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("count=%d want %d for query %s", got, want, query)
	}
}

func intString(v int64) string {
	return strconv.FormatInt(v, 10)
}
