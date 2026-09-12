//go:build integration_pg

package credentialstore

import (
	"context"
	"testing"
	"time"
)

// TestRotateClearsAuthCooldownLane:显式凭据轮换(导入更新 / 重新授权)在同一事务内清除该账号的
// auth 降级车道真相行(含硬禁),新凭据立即回到选号候选;刷新 worker 的成功刷新不清除。
// 判别:去掉 Rotate 里的删除语句 → 轮换后行仍在,第一段断言红;把删除挪进 SaveRefreshSuccess →
// 第二段断言红。
func TestRotateClearsAuthCooldownLane(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture := seedCredentialAuditTxFixture(t, ctx, pool, "auth-cooldown-rotate")
	t.Cleanup(func() { cleanupSubscriptionFixture(t, context.Background(), pool, fixture) })
	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())

	created, err := store.Create(ctx, CreateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeCodexCLIOAuth,
		Payload: []byte(`{"access_token":"access-1","refresh_token":"refresh-1"}`),
		ActorID: "owner",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	insertLaneRow := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO provider_account_auth_cooldowns (
				provider_account_id, tenant_id, strike, auth_until, hard_disabled, credential_version,
				last_failure_class, hard_disabled_at
			) VALUES ($1, $2, 3, NULL, true, 1, 'iron_clad', now())
			ON CONFLICT (provider_account_id) DO UPDATE SET hard_disabled = true, hard_disabled_at = now()`,
			fixture.providerAccountID, fixture.tenantID); err != nil {
			t.Fatalf("insert lane row: %v", err)
		}
	}
	laneRows := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM provider_account_auth_cooldowns WHERE provider_account_id = $1`,
			fixture.providerAccountID).Scan(&n); err != nil {
			t.Fatalf("count lane rows: %v", err)
		}
		return n
	}

	insertLaneRow()
	if _, err := store.Rotate(ctx, RotateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID, CredentialID: created.ID,
		Payload: []byte(`{"access_token":"access-2","refresh_token":"refresh-2"}`),
		ActorID: "owner",
	}); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if n := laneRows(); n != 0 {
		t.Fatalf("显式轮换后车道真相行必须被清除,实得 %d 行", n)
	}

	insertLaneRow()
	record, err := store.getRecord(ctx, fixture.tenantID, fixture.providerAccountID, created.ID, true)
	if err != nil {
		t.Fatalf("getRecord: %v", err)
	}
	if err := store.SaveRefreshSuccess(ctx, record,
		[]byte(`{"access_token":"access-3","refresh_token":"refresh-3"}`),
		time.Now().UTC().Add(time.Hour), "refresh_succeeded"); err != nil {
		t.Fatalf("SaveRefreshSuccess: %v", err)
	}
	if n := laneRows(); n != 1 {
		t.Fatalf("刷新成功不得清除车道硬禁,实得 %d 行", n)
	}
}
