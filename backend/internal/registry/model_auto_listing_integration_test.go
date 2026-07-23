//go:build integration_pg

package registry

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
)

// 上架管道第 3+4 关端到端:promote 一条 pending 发现后,模型自动绑到"能提供它"的池,
// 且【只】绑合格池(判别性):
//   - 账号 allow_list 含此模型 → 绑;
//   - 账号 allow_list 为空(=不限) → 绑;
//   - 账号 allow_list 只含别的模型 → 不绑。
// MUTATION: 删掉 promoteModelDiscoveryOnce 里的 AutoBindModelToEligiblePoolsTx 调用 → 前两条
// 断言红;删掉 AutoBind SQL 的 allow_list 谓词 → 第三条断言红。
func TestPromoteAutoBindsEligiblePoolsOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	modelID := "gpt-autobind-" + suffix

	// 复用同包整链 fixture(tenant→provider→pool_group→channel→account)。四租户四池四账号,
	// 覆盖"是否绑池"的四种资格态。
	tenantEligible, acctEligible := seedModelSyncTrackingAccount(t, ctx, pool, "ab-eligible-"+suffix, "openai")
	tenantAllowAll, acctAllowAll := seedModelSyncTrackingAccount(t, ctx, pool, "ab-allowall-"+suffix, "openai")
	tenantOther, acctOther := seedModelSyncTrackingAccount(t, ctx, pool, "ab-other-"+suffix, "openai")
	tenantNoCred, acctNoCred := seedModelSyncTrackingAccount(t, ctx, pool, "ab-nocred-"+suffix, "openai")
	t.Cleanup(func() {
		cctx := context.Background()
		for _, tid := range []int64{tenantEligible, tenantAllowAll, tenantOther, tenantNoCred} {
			_, _ = pool.Exec(cctx, `DELETE FROM model_pool_bindings WHERE tenant_id = $1`, tid)
			_, _ = pool.Exec(cctx, `DELETE FROM model_registry_snapshots WHERE tenant_id = $1`, tid)
			_, _ = pool.Exec(cctx, `DELETE FROM account_credentials WHERE tenant_id = $1`, tid)
		}
		// promote 产生的全局 model/alias/capability 与发现箱行按模型名清,防止残留污染
		// 共享库里其它 vendor-sync 测试(会把消失的 alias 判成 Disabled)。
		cleanupModelSyncTracking(t, cctx, pool, tenantEligible, modelID)
		cleanupModelSyncTracking(t, cctx, pool, tenantAllowAll, "ab-allowall-"+suffix)
		cleanupModelSyncTracking(t, cctx, pool, tenantOther, "ab-other-"+suffix)
		cleanupModelSyncTracking(t, cctx, pool, tenantNoCred, "ab-nocred-"+suffix)
	})
	// 白名单态:含此模型 / 空(=不限) / 只含别的模型 / 含此模型但【无有效凭据】。
	if _, err := pool.Exec(ctx, `UPDATE provider_accounts SET model_allow_list = $2 WHERE id = $1`,
		acctEligible, []string{modelID}); err != nil {
		t.Fatalf("set eligible allow_list: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE provider_accounts SET model_allow_list = $2 WHERE id = $1`,
		acctOther, []string{"some-other-model-" + suffix}); err != nil {
		t.Fatalf("set other allow_list: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE provider_accounts SET model_allow_list = $2 WHERE id = $1`,
		acctNoCred, []string{modelID}); err != nil {
		t.Fatalf("set nocred allow_list: %v", err)
	}
	// 资格门:只有有可服务凭据(active)的账号才触发持久绑定。给 eligible/allowall 各种一条
	// active 凭据;acctNoCred 刻意不种 → 其池不应被绑(专测 S0 修复的凭据资格谓词)。
	seedActiveCredential(t, ctx, pool, tenantEligible, acctEligible)
	seedActiveCredential(t, ctx, pool, tenantAllowAll, acctAllowAll)

	store := NewPostgresRegistry(pool, nil)
	// 第 1+2 关:vendor 目录发现 → 未知模型入 inbox pending。
	if _, err := store.ApplyVendorCatalog(ctx, modelsync.Catalog{
		Vendor: modelsync.VendorOpenAI,
		Models: []modelsync.Model{{ID: modelID, DisplayName: modelID, ProtocolFamily: "openai_chat"}},
	}, modelsync.ApplyOptions{Reason: "autobind-test", Actor: "test"}); err != nil {
		t.Fatalf("apply catalog: %v", err)
	}
	page, err := store.ListModelDiscoveries(ctx, ModelDiscoveryListParams{
		Access: ModelDiscoveryAccess{Role: modelAdminRolePlatform},
		Vendor: modelsync.VendorOpenAI, Status: ModelDiscoveryPending, Search: modelID,
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list pending: items=%d err=%v", len(page.Items), err)
	}

	// 第 3 关:promote(其内部应串起第 4 关自动绑池)。
	promoted, err := store.PromoteModelDiscovery(ctx, ModelDiscoveryDecision{
		Access: ModelDiscoveryAccess{Role: modelAdminRolePlatform, Actor: "admin_token:1", RequestID: "req-ab-" + suffix},
		ID:     page.Items[0].ID, Reason: "上架并自动绑池",
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted.PromotedModelID == nil {
		t.Fatal("promoted.PromotedModelID 为空")
	}

	boundTenants := map[int64]int{}
	rows, err := pool.Query(ctx, `
SELECT tenant_id, COUNT(*) FROM model_pool_bindings
WHERE model_id = $1 AND deleted_at IS NULL AND tenant_id IN ($2, $3, $4, $5)
GROUP BY tenant_id`, *promoted.PromotedModelID, tenantEligible, tenantAllowAll, tenantOther, tenantNoCred)
	if err != nil {
		t.Fatalf("query bindings: %v", err)
	}
	for rows.Next() {
		var tid int64
		var n int
		if err := rows.Scan(&tid, &n); err != nil {
			t.Fatal(err)
		}
		boundTenants[tid] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()

	if boundTenants[tenantEligible] != 1 {
		t.Errorf("allow_list 含此模型且有凭据的池应被自动绑定,got bindings=%d", boundTenants[tenantEligible])
	}
	if boundTenants[tenantAllowAll] != 1 {
		t.Errorf("空 allow_list(不限)且有凭据的池应被自动绑定,got bindings=%d", boundTenants[tenantAllowAll])
	}
	if boundTenants[tenantOther] != 0 {
		t.Errorf("allow_list 不含此模型的池不应被绑定,got bindings=%d", boundTenants[tenantOther])
	}
	if boundTenants[tenantNoCred] != 0 {
		t.Errorf("无有效凭据的账号所在池不应被绑定(S0 资格谓词),got bindings=%d", boundTenants[tenantNoCred])
	}

	// 绑定必须与 promote 同事务 bump 受影响租户快照(resolver 时点一致读依赖版本前进)。
	var snapshots int
	if err := pool.QueryRow(ctx, `
SELECT COUNT(*) FROM model_registry_snapshots WHERE tenant_id IN ($1, $2)`,
		tenantEligible, tenantAllowAll).Scan(&snapshots); err != nil {
		t.Fatalf("query snapshots: %v", err)
	}
	if snapshots != 2 {
		t.Errorf("受影响两租户应各有快照版本行,got %d", snapshots)
	}
}

// seedActiveCredential 给账号种一条 active 凭据(镜像 accountmodeldiscovery 集成 fixture 的
// account_credentials 插入),使账号满足绑池资格谓词的"存在可服务凭据"一门。
func seedActiveCredential(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, accountID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
INSERT INTO account_credentials (
    tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
    encrypted_payload, key_id, nonce, aad_hash
) VALUES ($1,$2,'openai','api_key','active',1,$3,'integration-key',$4,$5)`,
		tenantID, accountID, []byte{1}, []byte{2}, "integration-aad-"+strconv.FormatInt(accountID, 10)); err != nil {
		t.Fatalf("seed active credential: %v", err)
	}
}
