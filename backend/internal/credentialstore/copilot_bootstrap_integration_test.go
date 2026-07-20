//go:build integration_pg

package credentialstore

import (
	"context"
	"testing"
	"time"
)

// TestCopilotBootstrapSchedulesImmediateRefresh 验证长期 GitHub 授权材料即使带有
// 未来到期时间，只要尚未生成 Copilot 运行令牌，也必须立即进入刷新扫描。
func TestCopilotBootstrapSchedulesImmediateRefresh(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture := seedCredentialAuditTxFixture(t, ctx, pool, "copilot-bootstrap")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)

	now := time.Now().UTC()
	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	meta, err := store.Create(ctx, CreateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID,
		Vendor: VendorCopilot, AuthMode: AuthModeCopilotOAuth, ActorID: "owner",
		Payload: []byte(`{"github_access_token":"github-long-lived","expires_at":"2099-01-01T00:00:00Z"}`),
	})
	if err != nil {
		t.Fatalf("创建 Copilot 引导凭据：%v", err)
	}
	var refreshBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT refresh_before_at FROM account_credentials WHERE id=$1`, meta.ID).Scan(&refreshBefore); err != nil {
		t.Fatalf("读取 refresh_before_at：%v", err)
	}
	if refreshBefore.After(now.Add(5 * time.Second)) {
		t.Fatalf("引导凭据未立即排入刷新：refresh_before_at=%v now=%v", refreshBefore, now)
	}
}
