//go:build integration_pg

package billingmaint

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPruneSchedulerOutboxRows 验证 outbox 修剪的保留语义:
//   - 超龄未消费行(created_at 早于未消费截止)→ 删;
//   - 新鲜未消费行 → 留(未来消费者还要读);
//   - 已消费且消费时间早于已消费截止 → 删;
//   - 刚消费(排障保留期内)→ 留。
//
// 变异:删 created_at 谓词 → 超龄未消费行幸存 → 「超龄未消费已删」断言 RED;
// 删 consumed_at 谓词 → 已消费历史行幸存 → 「已消费历史已删」断言 RED;
// 删 LIMIT → 批量上限子测试(limit=1 只删 1 行)RED。
func TestPruneSchedulerOutboxRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping outbox prune integration test")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		fmt.Sprintf("outbox-prune-%d", time.Now().UnixNano())).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}

	now := time.Now().UTC()
	insertRow := func(created time.Time, consumed *time.Time) int64 {
		t.Helper()
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO scheduler_outbox (tenant_id, event_type, payload, created_at, consumed_at)
			VALUES ($1, 'account_quota_changed', '{}'::jsonb, $2, $3)
			RETURNING id`,
			tenantID, created, consumed,
		).Scan(&id); err != nil {
			t.Fatalf("插入 outbox 行: %v", err)
		}
		return id
	}

	staleConsumed := now.Add(-48 * time.Hour)
	freshConsumed := now.Add(-time.Minute)
	agedUnconsumedID := insertRow(now.Add(-8*24*time.Hour), nil)        // 超龄未消费 → 删
	freshUnconsumedID := insertRow(now.Add(-time.Hour), nil)            // 新鲜未消费 → 留
	staleConsumedID := insertRow(now.Add(-2*time.Hour), &staleConsumed) // 已消费历史 → 删
	freshConsumedID := insertRow(now.Add(-time.Hour), &freshConsumed)   // 刚消费 → 留

	q := New(tx)
	deleted, err := q.PruneSchedulerOutboxRows(ctx, PruneSchedulerOutboxRowsParams{
		CreatedBefore:  now.Add(-7 * 24 * time.Hour),
		ConsumedBefore: now.Add(-24 * time.Hour),
		BatchLimit:     100,
	})
	if err != nil {
		t.Fatalf("PruneSchedulerOutboxRows: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("应删 2 行(超龄未消费+已消费历史),实删 %d", deleted)
	}

	surviving := map[int64]bool{}
	rows, err := tx.Query(ctx, `SELECT id FROM scheduler_outbox WHERE tenant_id = $1`, tenantID)
	if err != nil {
		t.Fatalf("查幸存行: %v", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		surviving[id] = true
	}
	rows.Close()

	if surviving[agedUnconsumedID] {
		t.Fatal("超龄未消费行应被删除")
	}
	if surviving[staleConsumedID] {
		t.Fatal("已消费历史行应被删除")
	}
	if !surviving[freshUnconsumedID] {
		t.Fatal("新鲜未消费行不应被删除")
	}
	if !surviving[freshConsumedID] {
		t.Fatal("排障保留期内的已消费行不应被删除")
	}

	t.Run("批量上限生效", func(t *testing.T) {
		a := insertRow(now.Add(-9*24*time.Hour), nil)
		b := insertRow(now.Add(-9*24*time.Hour), nil)
		deleted, err := q.PruneSchedulerOutboxRows(ctx, PruneSchedulerOutboxRowsParams{
			CreatedBefore:  now.Add(-7 * 24 * time.Hour),
			ConsumedBefore: now.Add(-24 * time.Hour),
			BatchLimit:     1,
		})
		if err != nil {
			t.Fatalf("PruneSchedulerOutboxRows(limit=1): %v", err)
		}
		if deleted != 1 {
			t.Fatalf("limit=1 应只删 1 行,实删 %d(批量上限失效=积压一次性长事务)", deleted)
		}
		var remain int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM scheduler_outbox WHERE id IN ($1,$2)`, a, b).Scan(&remain); err != nil {
			t.Fatalf("count: %v", err)
		}
		if remain != 1 {
			t.Fatalf("两条超龄行应剩 1 条,实剩 %d", remain)
		}
	})
}
