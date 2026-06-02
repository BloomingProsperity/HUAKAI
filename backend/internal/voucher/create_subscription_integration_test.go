// HUAKAI · iKun
//go:build integration_pg

package voucher

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestPG_CreateSubscriptionVoucher_ViaService 经 Service.Create 建订阅券 (grant_kind=subscription + 套餐指针),
// 真 PG 落库, RETURNING 与直读库一致。这是 5d 订阅券创建端点的底层能力。
// 判别: INSERT 漏穿 grant_kind ($15) → 落库取列 DEFAULT 'balance' → grant_kind 断言变红
// (订阅券会被当余额券, 兑换时走 billing_events 余额路径 = 反向偷钱)。
func TestPG_CreateSubscriptionVoucher_ViaService(t *testing.T) {
	ctx := context.Background()
	pool := openSubPool(t, ctx)
	f := newSubVoucherFixture(t, ctx, pool)
	plan := f.seedPlan("sub-create-"+f.suffix, "premium", 30, capDec("5"))
	now := time.Now().UTC()

	res, err := f.svc.Create(ctx, CreateInput{
		TenantID: f.tenantID, AdminID: 1, Code: "SUBV-" + f.suffix,
		AmountCents: 1990, CurrencyCode: "USD",
		ValidFrom: now.Add(-time.Hour), ValidUntil: now.AddDate(0, 0, 30),
		MaxRedemptions: 1, SingleUsePerUser: true,
		GrantKind: GrantKindSubscription, SubscriptionPlanID: &plan.ID, Now: now,
	})
	if err != nil {
		t.Fatalf("create subscription voucher: %v", err)
	}
	if res.Voucher.GrantKind != GrantKindSubscription {
		t.Fatalf("grant_kind = %q, want subscription", res.Voucher.GrantKind)
	}
	if res.Voucher.SubscriptionPlanID == nil || *res.Voucher.SubscriptionPlanID != plan.ID {
		t.Fatalf("subscription_plan_id = %v, want %d", res.Voucher.SubscriptionPlanID, plan.ID)
	}
	// 直读库确认真持久 (不只信 RETURNING)。
	var gk string
	var spid int64
	if err := pool.QueryRow(ctx, `SELECT grant_kind, subscription_plan_id FROM voucher WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, res.Voucher.ID).Scan(&gk, &spid); err != nil {
		t.Fatalf("read back voucher: %v", err)
	}
	if gk != GrantKindSubscription || spid != plan.ID {
		t.Fatalf("persisted grant_kind=%q plan_id=%d, want subscription/%d", gk, spid, plan.ID)
	}
}

// TestPG_CreateBalanceVoucher_ViaService_Default GrantKind 省略 = 余额券, plan 指针 NULL, grant_kind 落 balance。
// 判别: normalizeGrantKind 不再把空值兜底为 balance (改返 "" 或别的) → validateCreateInput 落 unknown 分支
// → ErrInvalidInput → Create 失败变红。(store 层 grantKindOrDefault 兜底由 TestPG_CreateBatch_OnPG 守, 批量路径不过 normalize。)
func TestPG_CreateBalanceVoucher_ViaService_Default(t *testing.T) {
	ctx := context.Background()
	pool := openSubPool(t, ctx)
	f := newSubVoucherFixture(t, ctx, pool)
	now := time.Now().UTC()

	res, err := f.svc.Create(ctx, CreateInput{
		TenantID: f.tenantID, AdminID: 1, Code: "BALV-" + f.suffix,
		AmountCents: 500, CurrencyCode: "USD",
		ValidFrom: now.Add(-time.Hour), ValidUntil: now.AddDate(0, 0, 30),
		MaxRedemptions: 1, SingleUsePerUser: true, Now: now, // GrantKind 省略
	})
	if err != nil {
		t.Fatalf("create balance voucher (default kind): %v", err)
	}
	if res.Voucher.GrantKind != GrantKindBalance {
		t.Fatalf("grant_kind = %q, want balance (default)", res.Voucher.GrantKind)
	}
	if res.Voucher.SubscriptionPlanID != nil {
		t.Fatalf("balance voucher subscription_plan_id = %v, want nil", res.Voucher.SubscriptionPlanID)
	}
}

// TestPG_CreateSubscriptionVoucher_RequiresPlanID 订阅券缺套餐指针 → service 层先拦 ErrInvalidInput, 不落库。
// 判别: 去掉 validate 的 subscription-requires-plan 守卫 → 进 INSERT (plan_id NULL) →
// DB voucher_subscription_kind_check 违反 → err 是包裹的 DB 错而非 ErrInvalidInput → 红。
func TestPG_CreateSubscriptionVoucher_RequiresPlanID(t *testing.T) {
	ctx := context.Background()
	pool := openSubPool(t, ctx)
	f := newSubVoucherFixture(t, ctx, pool)
	now := time.Now().UTC()

	_, err := f.svc.Create(ctx, CreateInput{
		TenantID: f.tenantID, AdminID: 1, Code: "SUBNOPLAN-" + f.suffix,
		AmountCents: 1990, CurrencyCode: "USD",
		ValidFrom: now.Add(-time.Hour), ValidUntil: now.AddDate(0, 0, 30),
		MaxRedemptions: 1, GrantKind: GrantKindSubscription, Now: now, // SubscriptionPlanID nil
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput (subscription requires plan_id)", err)
	}
}

// TestPG_CreateBalanceVoucher_RejectsPlanID 余额券携带套餐指针 = 误配 → ErrInvalidInput (防余额券错挂到套餐)。
// 判别: 去掉 validate 的 balance-rejects-plan 守卫 → 进 INSERT (balance + 不存在的 plan_id) →
// FK voucher_subscription_plan_fk 违反 → err 非 ErrInvalidInput → 红。
func TestPG_CreateBalanceVoucher_RejectsPlanID(t *testing.T) {
	ctx := context.Background()
	pool := openSubPool(t, ctx)
	f := newSubVoucherFixture(t, ctx, pool)
	now := time.Now().UTC()
	bogus := int64(999999)

	_, err := f.svc.Create(ctx, CreateInput{
		TenantID: f.tenantID, AdminID: 1, Code: "BALPLAN-" + f.suffix,
		AmountCents: 500, CurrencyCode: "USD",
		ValidFrom: now.Add(-time.Hour), ValidUntil: now.AddDate(0, 0, 30),
		MaxRedemptions: 1, GrantKind: GrantKindBalance, SubscriptionPlanID: &bogus, Now: now,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput (balance must not carry plan_id)", err)
	}
}
