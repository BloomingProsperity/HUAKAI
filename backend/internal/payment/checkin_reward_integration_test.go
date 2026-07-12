//go:build integration_pg

package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestCheckinAtomic_CreditFailureRollsBack(t *testing.T) {
	// 变异:在 daily_checkin 插入之后、payment credit 之前就提交,会让"孤儿行"断言变红。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	orig := insertCheckinTopupCreditTx
	insertCheckinTopupCreditTx = func(context.Context, pgx.Tx, Order, string, int64, string, time.Time) (CreditRecord, int64, error) {
		return CreditRecord{}, 0, errors.New("forced checkin credit failure")
	}
	t.Cleanup(func() { insertCheckinTopupCreditTx = orig })

	svc := NewService(NewPostgresStore(pool))
	_, err := svc.ApplyCheckinReward(ctx, CheckinRewardInput{
		TenantID:     f.tenantA,
		UserID:       f.userA,
		Date:         time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		RewardCents:  11,
		CurrencyCode: "USD",
		Now:          time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("ApplyCheckinReward with forced credit failure returned nil error")
	}
	if n := f.countInt(`SELECT count(*) FROM daily_checkin WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); n != 0 {
		t.Fatalf("daily_checkin rows after failed credit=%d want 0", n)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); n != 0 {
		t.Fatalf("payment_credits after failed credit=%d want 0", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_credited'`, f.tenantA); n != 0 {
		t.Fatalf("payment_credited events after failed credit=%d want 0", n)
	}
}
