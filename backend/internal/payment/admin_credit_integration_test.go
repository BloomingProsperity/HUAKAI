//go:build integration_pg

package payment

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestPostgresStoreAdminAdjustBalanceAttributesNumericTokenID 跨层真路径守 P2b-1 回归:
// 真 Service.AdminAdjustBalance(真库,非 mock)喂纯数字 TokenID("11") →
// payment_orders.created_by_admin_id / confirmed_by_admin_id 与 payment_audit_events.actor_id
// 三处归属列都必须落成数字 11(不是 0 / NULL)。
//
// 说明(与任务措辞的表名对应):HUAKAI 实际写的钱表是 payment_orders(任务口径 recharge_orders)
// 与 payment_audit_events(任务口径 payment_audit_log,后者是遗留兼容表)。归属列断言在这两张真表上。
//
// 这条捕捉 P2b-1 handler 单测(mock 掉 Service、只验传入串)所无法捕捉的下游 ParseInt→列值缺口:
// 若 handler 回退成传 AuditActor() 的 "admin_token:11",parseAdminActorID 得 0 → nullableInt64→NULL,
// 本测三处归属断言全红。
func TestPostgresStoreAdminAdjustBalanceAttributesNumericTokenID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	// 纯数字 TokenID —— 生产 handler(balance_credit)P2b-1 修复后传的正是 fmt.Sprintf("%d", ident.TokenID)。
	const wantAdminID = int64(11)
	credit, err := svc.AdminAdjustBalance(ctx, AdminBalanceAdjustmentInput{
		TenantID:        f.tenantA,
		UserID:          f.userA,
		Amount:          decimal.RequireFromString("200.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "11",
		Reason:          "owner-approved manual recharge attribution",
		ExternalTradeNo: "admin-credit-attr-200",
		Now:             time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AdminAdjustBalance +200: %v", err)
	}
	if credit.RechargeOrderID == 0 {
		t.Fatal("positive admin recharge must create a recharge order")
	}

	// 订单归属两列:建单归属(created_by_admin_id)+ 确认归属(confirmed_by_admin_id)。
	assertOrderAdminAttribution(t, ctx, pool, f.tenantA, credit.RechargeOrderID, wantAdminID, wantAdminID)
	// 审计事件归属:AuditCredited(入账)那条 actor_id 必须是数字 TokenID,不能 NULL。
	assertAuditEventActorID(t, ctx, pool, f.tenantA, credit.RechargeOrderID, AuditCredited, wantAdminID)
	// AuditOrderCreated / AuditPaidConfirmed 也应带同一归属(穷举归属落库的三个审计写点)。
	assertAuditEventActorID(t, ctx, pool, f.tenantA, credit.RechargeOrderID, AuditOrderCreated, wantAdminID)
	assertAuditEventActorID(t, ctx, pool, f.tenantA, credit.RechargeOrderID, AuditPaidConfirmed, wantAdminID)
}

// TestPostgresStoreAdminAdjustBalancePoisonedActorLosesAttribution 把「传字符串会丢归属」这个
// 跨层坑钉成回归:喂一个非数字 actor 串("admin_token:5")→ parseAdminActorID 得 0 →
// 归属确实变成 0 / NULL(三处归属列)。
//
// 目的是防未来有人再把 AuditActor() 接进 balance_credit:这条测试文档化并锁死了
// 「非数字 actor → 归属丢失」的当前(已知有害的)行为。它是 P2b-1 回归的负向镜像断言。
func TestPostgresStoreAdminAdjustBalancePoisonedActorLosesAttribution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	// AuditActor() 形态:parseAdminActorID(strconv.ParseInt)吞错 → 0。
	credit, err := svc.AdminAdjustBalance(ctx, AdminBalanceAdjustmentInput{
		TenantID:        f.tenantA,
		UserID:          f.userA,
		Amount:          decimal.RequireFromString("70.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin_token:5",
		Reason:          "poisoned non-numeric actor drops attribution (P2b-1 regression mirror)",
		ExternalTradeNo: "admin-credit-poison-70",
		Now:             time.Date(2026, 6, 3, 9, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AdminAdjustBalance +70 poisoned actor: %v", err)
	}
	if credit.RechargeOrderID == 0 {
		t.Fatal("positive admin recharge must create a recharge order even with poisoned actor")
	}

	// 归属确实丢:两个订单归属列 + 审计 actor_id 都是 0 / NULL。
	// (assertOrderAdminAttribution / assertAuditEventActorID 传 0 → 断言列为 NULL。)
	assertOrderAdminAttribution(t, ctx, pool, f.tenantA, credit.RechargeOrderID, 0, 0)
	assertAuditEventActorID(t, ctx, pool, f.tenantA, credit.RechargeOrderID, AuditCredited, 0)
}

// assertOrderAdminAttribution 断言 payment_orders 一行的建单/确认归属列。
// wantCreated/wantConfirmed==0 表示期望该列为 NULL(归属丢失);>0 表示期望等于该数字 admin id。
func assertOrderAdminAttribution(t *testing.T, ctx context.Context, pool queryPool, tenantID, orderID, wantCreated, wantConfirmed int64) {
	t.Helper()
	var created, confirmed sql.NullInt64
	if err := pool.QueryRow(ctx,
		`SELECT created_by_admin_id, confirmed_by_admin_id FROM payment_orders WHERE tenant_id=$1 AND id=$2`,
		tenantID, orderID,
	).Scan(&created, &confirmed); err != nil {
		t.Fatalf("read order admin attribution: %v", err)
	}
	assertNullInt64Attribution(t, "created_by_admin_id", created, wantCreated)
	assertNullInt64Attribution(t, "confirmed_by_admin_id", confirmed, wantConfirmed)
}

// assertAuditEventActorID 断言指定 event_type 的 payment_audit_events 行的 actor_id。
// want==0 表示期望 actor_id 为 NULL(归属丢失);>0 表示等于该数字 admin id。
func assertAuditEventActorID(t *testing.T, ctx context.Context, pool queryPool, tenantID, orderID int64, eventType string, want int64) {
	t.Helper()
	var actorID sql.NullInt64
	if err := pool.QueryRow(ctx,
		`SELECT actor_id FROM payment_audit_events WHERE tenant_id=$1 AND payment_order_id=$2 AND event_type=$3 ORDER BY id LIMIT 1`,
		tenantID, orderID, eventType,
	).Scan(&actorID); err != nil {
		t.Fatalf("read audit event %s actor_id: %v", eventType, err)
	}
	assertNullInt64Attribution(t, "audit_events["+eventType+"].actor_id", actorID, want)
}

func assertNullInt64Attribution(t *testing.T, col string, got sql.NullInt64, want int64) {
	t.Helper()
	if want == 0 {
		if got.Valid {
			t.Fatalf("%s=%d want NULL (非数字 actor 应丢归属)", col, got.Int64)
		}
		return
	}
	if !got.Valid {
		t.Fatalf("%s=NULL want %d (数字 TokenID 归属被抹成 NULL —— P2b-1 回归)", col, want)
	}
	if got.Int64 != want {
		t.Fatalf("%s=%d want %d", col, got.Int64, want)
	}
}

func TestPostgresStoreAdminAdjustBalanceCreditsAuditsAndRejectsDebitWithoutBalanceMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	credit, err := svc.AdminAdjustBalance(ctx, AdminBalanceAdjustmentInput{
		TenantID:        f.tenantA,
		UserID:          f.userA,
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
	assertUserBalanceText(t, ctx, pool, f.tenantA, f.userA, "200.00000000")
	assertPaymentAuditEventCount(t, ctx, pool, f.tenantA, credit.RechargeOrderID, AuditCredited, 1)
	assertPaymentCreditedEventCount(t, ctx, pool, f.tenantA, credit.RechargeOrderID, 1)

	_, err = svc.AdminAdjustBalance(ctx, AdminBalanceAdjustmentInput{
		TenantID:        f.tenantA,
		UserID:          f.userA,
		Amount:          decimal.RequireFromString("-50.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          "conflicting debit must preserve idempotency semantics",
		ExternalTradeNo: "admin-credit-200",
		Now:             time.Date(2026, 6, 1, 11, 3, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrExternalTradeConflict) {
		t.Fatalf("AdminAdjustBalance debit with credited key err=%v want ErrExternalTradeConflict", err)
	}
	assertUserBalanceText(t, ctx, pool, f.tenantA, f.userA, "200.00000000")

	_, err = svc.AdminAdjustBalance(ctx, AdminBalanceAdjustmentInput{
		TenantID:        f.tenantA,
		UserID:          f.userA,
		Amount:          decimal.RequireFromString("-250.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          "operator debit clamp regression fixture",
		ExternalTradeNo: "admin-debit-250",
		Now:             time.Date(2026, 6, 1, 11, 5, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrAdminDebitNotSupported) {
		t.Fatalf("AdminAdjustBalance -250 err=%v want ErrAdminDebitNotSupported", err)
	}
	assertUserBalanceText(t, ctx, pool, f.tenantA, f.userA, "200.00000000")
	assertAdminAdjustmentOrderCount(t, ctx, pool, f.tenantA, "admin-debit-250", 0)
}

func TestPostgresStoreAdminAdjustBalanceReplaysLegacyDebitBeforeGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	now := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)

	if _, err := pool.Exec(ctx, `
INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
VALUES ($1, $2, $3, 0, 1, $4)`,
		f.tenantA, f.userA, decimal.RequireFromString("150.00000000"), now,
	); err != nil {
		t.Fatalf("seed legacy balance: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO payment_audit_log (
	tenant_id, recharge_order_id, user_id, provider, external_trade_no,
	provider_event_id, outcome, reason, paid_amount, expected_amount,
	currency_code, metadata, created_at
) VALUES (
	$1, NULL, $2, $3, $4,
	$5, $6, $7, $8, $8,
	$9, jsonb_build_object('audit_code', $5::text), $10
)`,
		f.tenantA, f.userA, adminPaymentProvider, "legacy-admin-debit-50",
		adminAuditAdjustment, AuditOutcomeAccepted, AuditReasonCompleted,
		decimal.RequireFromString("-50.00000000"), "USD", now,
	); err != nil {
		t.Fatalf("seed legacy debit audit: %v", err)
	}

	svc := NewService(NewPostgresStore(pool))
	replay, err := svc.AdminAdjustBalance(ctx, AdminBalanceAdjustmentInput{
		TenantID:        f.tenantA,
		UserID:          f.userA,
		Amount:          decimal.RequireFromString("-50.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          "legacy debit replay",
		ExternalTradeNo: "legacy-admin-debit-50",
		Now:             now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("AdminAdjustBalance legacy debit replay: %v", err)
	}
	if !replay.NewBalance.Equal(decimal.RequireFromString("150.00000000")) {
		t.Fatalf("legacy debit replay NewBalance=%s want 150.00000000", replay.NewBalance)
	}
	assertUserBalanceText(t, ctx, pool, f.tenantA, f.userA, "150.00000000")
	assertLegacyManualAuditCodeCount(t, ctx, pool, f.tenantA, f.userA, "MANUAL_BALANCE_ADJUSTMENT", 1)
}

func TestPostgresStoreAdminAdjustBalanceIsIdempotentByExternalTradeNo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	creditInput := AdminBalanceAdjustmentInput{
		TenantID:        f.tenantA,
		UserID:          f.userA,
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
	assertUserBalanceText(t, ctx, pool, f.tenantA, f.userA, "200.00000000")
	assertPaymentAuditEventCount(t, ctx, pool, f.tenantA, firstCredit.RechargeOrderID, AuditCredited, 1)
	assertPaymentCreditedEventCount(t, ctx, pool, f.tenantA, firstCredit.RechargeOrderID, 1)

	debitInput := AdminBalanceAdjustmentInput{
		TenantID:        f.tenantA,
		UserID:          f.userA,
		Amount:          decimal.RequireFromString("-50.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          "idempotent manual debit",
		ExternalTradeNo: "admin-idem-debit-50",
		Now:             time.Date(2026, 6, 1, 13, 5, 0, 0, time.UTC),
	}
	if _, err := svc.AdminAdjustBalance(ctx, debitInput); !errors.Is(err, ErrAdminDebitNotSupported) {
		t.Fatalf("first AdminAdjustBalance debit err=%v want ErrAdminDebitNotSupported", err)
	}
	if _, err := svc.AdminAdjustBalance(ctx, debitInput); !errors.Is(err, ErrAdminDebitNotSupported) {
		t.Fatalf("second AdminAdjustBalance debit err=%v want ErrAdminDebitNotSupported", err)
	}
	assertUserBalanceText(t, ctx, pool, f.tenantA, f.userA, "200.00000000")
	assertAdminAdjustmentOrderCount(t, ctx, pool, f.tenantA, "admin-idem-debit-50", 0)
}

func assertLegacyManualAuditCodeCount(t *testing.T, ctx context.Context, pool queryPool, tenantID, userID int64, code string, want int) {
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

func assertAdminAdjustmentOrderCount(t *testing.T, ctx context.Context, pool queryPool, tenantID int64, outTradeNo string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`,
		tenantID, outTradeNo,
	).Scan(&count); err != nil {
		t.Fatalf("count admin adjustment order %s: %v", outTradeNo, err)
	}
	if count != want {
		t.Fatalf("admin adjustment order %s count=%d want %d", outTradeNo, count, want)
	}
}
