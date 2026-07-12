//go:build integration_pg

package billing

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestListProviderAccountRecentRequestsScopesAccountAndTenantAndOrdersNewestFirst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fixture := seedObsMiscFixture(t, ctx, tx)
	base := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		seedObsMiscUsage(t, ctx, tx, fixture, fixture.providerAccountAID, fmt.Sprintf("recent-a-%d", i), base.Add(time.Duration(i)*time.Minute), 100+i, 10+i, "0.01000000")
		seedObsMiscUsage(t, ctx, tx, fixture, fixture.providerAccountBID, fmt.Sprintf("recent-b-%d", i), base.Add(time.Duration(i)*time.Minute), 200+i, 20+i, "0.02000000")
	}
	var otherTenantID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, fmt.Sprintf("recent-other-%d", time.Now().UnixNano())).Scan(&otherTenantID); err != nil {
		t.Fatalf("插入另一租户: %v", err)
	}

	rows, err := New(tx).ListProviderAccountRecentRequests(ctx, ListProviderAccountRecentRequestsParams{
		ProviderAccountID: fixture.providerAccountAID,
		TenantID:          fixture.tenantID,
		RowLimit:          20,
	})
	if err != nil {
		t.Fatalf("查询目标账号: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("目标账号行数=%d，期望=3；删除 provider_account_id 谓词会混入另一账号", len(rows))
	}
	wantTokens := []int32{103, 102, 101}
	for i, row := range rows {
		if row.TokensInput != wantTokens[i] {
			t.Fatalf("第 %d 行 tokens_input=%d，期望=%d；账号隔离或倒序失效", i, row.TokensInput, wantTokens[i])
		}
		if i > 0 && !rows[i-1].SettledAt.Time.After(row.SettledAt.Time) {
			t.Fatalf("settled_at 未严格降序：%s 后为 %s", rows[i-1].SettledAt.Time, row.SettledAt.Time)
		}
	}

	crossTenant, err := New(tx).ListProviderAccountRecentRequests(ctx, ListProviderAccountRecentRequestsParams{
		ProviderAccountID: fixture.providerAccountAID,
		TenantID:          otherTenantID,
		RowLimit:          20,
	})
	if err != nil {
		t.Fatalf("查询错配租户: %v", err)
	}
	if len(crossTenant) != 0 {
		t.Fatalf("错配租户返回 %d 行，期望 0；删除 tenant_id 谓词会泄露目标账号记录", len(crossTenant))
	}
}
