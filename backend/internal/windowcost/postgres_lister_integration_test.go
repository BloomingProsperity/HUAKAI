//go:build integration_pg

package windowcost

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// TestPostgresLister_ExcludesEndedAndUnlimitedWindows 抓对抗 bug-hunt S2:
// ListLimitedAccounts 必须只返回**窗口仍活动**的限额账号。已结束的 5h 窗口账号(end<now)绝不能返回,
// 否则 worker 会以陈旧 windowStart 聚合上一个窗口整 5 小时的历史花费、持续误封一个本应进入全新空窗口
// 的健康账号(违反本包"绝不错误下线健康账号"的安全不变量)。
// 变异(已验证转红):去掉 lister SQL 的 `session_window_5h_end > now()` → 已结束窗口账号被返回 → endedID 断言红。
func TestPostgresLister_ExcludesEndedAndUnlimitedWindows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)

	tenantID := seedWindowcostTenant(t, ctx, pool)
	now := time.Now()
	// 活动窗口 + 限额>0 → 应返回
	activeID := seedWindowcostAccount(t, ctx, pool, tenantID, 100, now.Add(-2*time.Hour), now.Add(3*time.Hour))
	// 已结束窗口 + 限额>0 → 不应返回(本 bug 的核心场景)
	endedID := seedWindowcostAccount(t, ctx, pool, tenantID, 100, now.Add(-6*time.Hour), now.Add(-1*time.Hour))
	// 限额=0(未启用)→ 不应返回(控制项,证明 limit 过滤仍在)
	unlimitedID := seedWindowcostAccount(t, ctx, pool, tenantID, 0, now.Add(-2*time.Hour), now.Add(3*time.Hour))

	recs, err := NewPostgresLister(pool).ListLimitedAccounts(ctx)
	if err != nil {
		t.Fatalf("ListLimitedAccounts: %v", err)
	}
	got := map[int64]bool{}
	for _, r := range recs {
		got[r.ID] = true
	}
	if !got[activeID] {
		t.Fatalf("活动窗口限额账号 %d 应被列出, recs=%+v", activeID, recs)
	}
	if got[endedID] {
		t.Fatalf("已结束窗口账号 %d 不应被列出(其历史花费会持续误封健康账号)", endedID)
	}
	if got[unlimitedID] {
		t.Fatalf("limit=0 账号 %d 不应被列出", unlimitedID)
	}
}

func seedWindowcostTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "wc-"+uuid.NewString()).Scan(&id); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, id) })
	return id
}

func seedWindowcostAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, limitCents int64, winStart, winEnd time.Time) int64 {
	t.Helper()
	suffix := uuid.NewString()
	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, "wc-"+suffix, "P "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "wc-pg-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	var channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "wc-ch-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
		    tenant_id, provider_id, channel_id, name, account_type,
		    window_cost_limit_cents, session_window_5h_start, session_window_5h_end
		 ) VALUES ($1, $2, $3, $4, 'api_key', $5, $6, $7) RETURNING id`,
		tenantID, providerID, channelID, "wc-acct-"+suffix, limitCents, winStart, winEnd,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	return accountID
}
