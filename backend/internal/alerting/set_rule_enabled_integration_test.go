//go:build integration_pg

package alerting

import (
	"context"
	"testing"
	"time"
)

// TestSetRuleEnabledInTxTogglesAndIsolatesTenant 用真 PG 验证 SetRuleEnabledInTx:
//   - 在传入的 tx 内翻转单条规则的 enabled,提交后状态真持久;
//   - 租户 scope 绑死在 SQL WHERE —— 拿别租户的 id(或同 id 但错租户)调用必须 0 行命中
//     (ErrNotFound),绝不跨租户改动。
//
// MUTATION(已心算验证转红):
//   - 把 SetRuleEnabledInTx 的 SQL 里 `tenant_id=$1 AND` 去掉 → 用租户 A 调租户 B 的规则会命中、
//     翻动 B 的规则 → 下面"跨租户必须 ErrNotFound"与"B 规则未被动"两处断言转红;
//   - 把 `enabled=$3` 写死成某常量 → "enable 后为 true / disable 后为 false"断言转红;
//   - 用 s.pool 而非传入 tx 执行 → 提交前回滚的子测试里"未提交不可见"断言转红。
func TestSetRuleEnabledInTxTogglesAndIsolatesTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openAlertingPool(t, ctx)
	tenantA := seedAlertingTenant(t, ctx, pool, "setenabled-a")
	tenantB := seedAlertingTenant(t, ctx, pool, "setenabled-b")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM alert_rules WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1,$2)`, tenantA, tenantB)
	})

	store := NewPostgresStore(pool)
	svc := NewService(store)
	ruleA := mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      tenantA,
		Name:          "rule a",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})
	ruleB := mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      tenantB,
		Name:          "rule b",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})

	// --- 在 tx 内 disable 租户 A 的规则,提交,验证真持久成 false。
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	got, err := store.SetRuleEnabledInTx(ctx, tx, tenantA, ruleA.ID, false)
	if err != nil {
		t.Fatalf("SetRuleEnabledInTx disable: %v", err)
	}
	if got.Enabled != false || got.ID != ruleA.ID || got.TenantID != tenantA {
		t.Fatalf("disabled rule=%+v want enabled=false id=%d tenant=%d", got, ruleA.ID, tenantA)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	reloaded, err := store.GetRule(ctx, tenantA, ruleA.ID)
	if err != nil {
		t.Fatalf("GetRule after commit: %v", err)
	}
	if reloaded.Enabled != false {
		t.Fatalf("rule A enabled=%v after committed disable want false", reloaded.Enabled)
	}

	// --- enable 回 true,再次验证翻转双向生效。
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	if _, err := store.SetRuleEnabledInTx(ctx, tx2, tenantA, ruleA.ID, true); err != nil {
		t.Fatalf("SetRuleEnabledInTx enable: %v", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("commit tx2: %v", err)
	}
	reloaded, err = store.GetRule(ctx, tenantA, ruleA.ID)
	if err != nil {
		t.Fatalf("GetRule after re-enable: %v", err)
	}
	if reloaded.Enabled != true {
		t.Fatalf("rule A enabled=%v after committed enable want true", reloaded.Enabled)
	}

	// --- 跨租户隔离:用租户 A 去翻租户 B 的规则 id,SQL WHERE tenant_id 必须 0 行命中 → ErrNotFound,
	// 且租户 B 的规则状态绝不被动。
	tx3, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx3: %v", err)
	}
	_, crossErr := store.SetRuleEnabledInTx(ctx, tx3, tenantA, ruleB.ID, false)
	// 先无条件回滚再断言 —— 这样即便断言失败(变异场景:谓词被删→跨租户命中并改了 B),
	// 也不会留下一个持锁的开放事务把后续 cleanup 卡死。
	_ = tx3.Rollback(ctx)
	if crossErr == nil || crossErr != ErrNotFound {
		t.Fatalf("cross-tenant SetRuleEnabledInTx err=%v want ErrNotFound (租户 scope 必须绑死在 SQL WHERE)", crossErr)
	}
	stillB, err := store.GetRule(ctx, tenantB, ruleB.ID)
	if err != nil {
		t.Fatalf("GetRule tenant B: %v", err)
	}
	if stillB.Enabled != true {
		t.Fatalf("rule B enabled=%v want true (must NOT be touched by tenant A's call)", stillB.Enabled)
	}

	// --- 未提交不可见:在 tx 内 disable 但回滚,A 的规则应仍为 true(证明写绑定到传入 tx 而非 s.pool)。
	tx4, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx4: %v", err)
	}
	if _, err := store.SetRuleEnabledInTx(ctx, tx4, tenantA, ruleA.ID, false); err != nil {
		t.Fatalf("SetRuleEnabledInTx in-tx disable: %v", err)
	}
	if err := tx4.Rollback(ctx); err != nil {
		t.Fatalf("rollback tx4: %v", err)
	}
	afterRollback, err := store.GetRule(ctx, tenantA, ruleA.ID)
	if err != nil {
		t.Fatalf("GetRule after rollback: %v", err)
	}
	if afterRollback.Enabled != true {
		t.Fatalf("rule A enabled=%v after rolled-back disable want true (写必须绑定到传入 tx)", afterRollback.Enabled)
	}
}
