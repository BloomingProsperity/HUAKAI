//go:build integration_pg

package payment

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestPostgresStoreAdminAdjustBalanceCreditsAuditsAndClampsDebit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentPool(t, ctx)
	tenantID, userID := seedPaymentUser(t, ctx, pool, "admin-credit")

	svc := NewService(NewPostgresStore(pool))
	credit, err := svc.AdminAdjustBalance(ctx, AdminBalanceAdjustmentInput{
		TenantID:        tenantID,
		UserID:          userID,
		Amount:          decimal.RequireFromString("200.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          "owner-approved manual recharge",
		ExternalTradeNo: "admin-credit-200",
		Now:             time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AdminAdjustBalance +200: %v", err)
	}
	if !credit.NewBalance.Equal(decimal.RequireFromString("200.00000000")) {
		t.Fatalf("credit NewBalance=%s want 200.00000000", credit.NewBalance)
	}
	if credit.RechargeOrderID == 0 {
		t.Fatal("positive admin recharge must create a recharge order for billing_events correlation")
	}
	assertUserBalanceText(t, ctx, pool, tenantID, userID, "200.00000000")
	assertManualAuditCodeCount(t, ctx, pool, tenantID, userID, "RECHARGE_SUCCESS", 1)
	assertBalanceRechargedEventCount(t, ctx, pool, tenantID, credit.RechargeOrderID, 1)

	debit, err := svc.AdminAdjustBalance(ctx, AdminBalanceAdjustmentInput{
		TenantID:        tenantID,
		UserID:          userID,
		Amount:          decimal.RequireFromString("-250.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          "operator debit clamp regression fixture",
		ExternalTradeNo: "admin-debit-250",
		Now:             time.Date(2026, 6, 1, 11, 5, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AdminAdjustBalance -250: %v", err)
	}
	if !debit.NewBalance.Equal(decimal.Zero) {
		t.Fatalf("debit NewBalance=%s want 0; removing clamp would produce -50", debit.NewBalance)
	}
	assertUserBalanceText(t, ctx, pool, tenantID, userID, "0.00000000")
}

func TestPostgresStoreAdminAdjustBalanceIsIdempotentByExternalTradeNo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentPool(t, ctx)
	tenantID, userID := seedPaymentUser(t, ctx, pool, "admin-idempotent")

	svc := NewService(NewPostgresStore(pool))
	creditInput := AdminBalanceAdjustmentInput{
		TenantID:        tenantID,
		UserID:          userID,
		Amount:          decimal.RequireFromString("200.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          "idempotent manual recharge",
		ExternalTradeNo: "admin-idem-credit-200",
		Now:             time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC),
	}
	firstCredit, err := svc.AdminAdjustBalance(ctx, creditInput)
	if err != nil {
		t.Fatalf("first AdminAdjustBalance credit: %v", err)
	}
	secondCredit, err := svc.AdminAdjustBalance(ctx, creditInput)
	if err != nil {
		t.Fatalf("second AdminAdjustBalance credit retry: %v", err)
	}
	if !firstCredit.NewBalance.Equal(decimal.RequireFromString("200.00000000")) ||
		!secondCredit.NewBalance.Equal(decimal.RequireFromString("200.00000000")) {
		t.Fatalf("credit retry balances first=%s second=%s want both 200", firstCredit.NewBalance, secondCredit.NewBalance)
	}
	assertUserBalanceText(t, ctx, pool, tenantID, userID, "200.00000000")
	assertManualAuditCodeCount(t, ctx, pool, tenantID, userID, "RECHARGE_SUCCESS", 1)
	assertBalanceRechargedEventCount(t, ctx, pool, tenantID, firstCredit.RechargeOrderID, 1)

	debitInput := AdminBalanceAdjustmentInput{
		TenantID:        tenantID,
		UserID:          userID,
		Amount:          decimal.RequireFromString("-50.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          "idempotent manual debit",
		ExternalTradeNo: "admin-idem-debit-50",
		Now:             time.Date(2026, 6, 1, 13, 5, 0, 0, time.UTC),
	}
	firstDebit, err := svc.AdminAdjustBalance(ctx, debitInput)
	if err != nil {
		t.Fatalf("first AdminAdjustBalance debit: %v", err)
	}
	secondDebit, err := svc.AdminAdjustBalance(ctx, debitInput)
	if err != nil {
		t.Fatalf("second AdminAdjustBalance debit retry: %v", err)
	}
	if !firstDebit.NewBalance.Equal(decimal.RequireFromString("150.00000000")) ||
		!secondDebit.NewBalance.Equal(decimal.RequireFromString("150.00000000")) {
		t.Fatalf("debit retry balances first=%s second=%s want both 150", firstDebit.NewBalance, secondDebit.NewBalance)
	}
	assertUserBalanceText(t, ctx, pool, tenantID, userID, "150.00000000")
	assertManualAuditCodeCount(t, ctx, pool, tenantID, userID, "MANUAL_BALANCE_ADJUSTMENT", 1)
}

func assertManualAuditCodeCount(t *testing.T, ctx context.Context, pool queryPool, tenantID, userID int64, code string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM payment_audit_log
WHERE tenant_id=$1
  AND user_id=$2
  AND provider_event_id=$3
  AND metadata->>'audit_code'=$3`,
		tenantID, userID, code,
	).Scan(&count); err != nil {
		t.Fatalf("count payment audit code %s: %v", code, err)
	}
	if count != want {
		t.Fatalf("payment audit code %s count=%d want %d", code, count, want)
	}
}
