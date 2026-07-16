//go:build integration_pg

package billing

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestListEligibleAccountsByPoolGroupFiltersExpiredAccounts 验证选号候选的到期语义。
// 变异:删除 expires_at WHERE 谓词后,过去已到期账号会重新出现在候选中,
// “已过期账号”子测试必须变红;NULL 到期账号用于防止过滤条件误伤永久账号。
func TestListEligibleAccountsByPoolGroupFiltersExpiredAccounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启账号候选测试事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	suffix := fmt.Sprintf("eligible-expiry-%d", time.Now().UnixNano())
	var tenantID, providerID, poolGroupID, channelID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"tenant-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, 'anthropic_messages') RETURNING id`,
		tenantID, "provider-"+suffix, "Provider "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("插入 pool group: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("插入 channel: %v", err)
	}

	insertAccount := func(name string, expiresAt any) int64 {
		t.Helper()
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO provider_accounts (
				tenant_id, provider_id, channel_id, name, account_type,
				health_state, credential_state, expires_at
			)
			VALUES ($1, $2, $3, $4, 'api_key', 'healthy', 'valid', $5)
			RETURNING id`,
			tenantID, providerID, channelID, name+"-"+suffix, expiresAt,
		).Scan(&id); err != nil {
			t.Fatalf("插入账号 %s: %v", name, err)
		}
		return id
	}

	expiredID := insertAccount("expired", time.Now().UTC().Add(-time.Hour))
	withoutExpiryID := insertAccount("without-expiry", nil)

	rows, err := New(tx).ListEligibleAccountsByPoolGroup(ctx, ListEligibleAccountsByPoolGroupParams{
		TenantID:                tenantID,
		PoolGroupID:             poolGroupID,
		RequestedModel:          "claude-test",
		RequestedProtocolFamily: "anthropic_messages",
		RequiredCapabilities:    []string{},
	})
	if err != nil {
		t.Fatalf("查询账号候选: %v", err)
	}
	returned := make(map[int64]bool, len(rows))
	for _, row := range rows {
		returned[row.ID] = true
	}

	t.Run("已过期账号不在选号结果中", func(t *testing.T) {
		if returned[expiredID] {
			t.Fatalf("已过期账号 id=%d 仍出现在选号候选中", expiredID)
		}
	})
	t.Run("NULL 到期时间账号仍在选号结果中", func(t *testing.T) {
		if !returned[withoutExpiryID] {
			t.Fatalf("未设过期时间的账号 id=%d 被错误排除", withoutExpiryID)
		}
	})
}
