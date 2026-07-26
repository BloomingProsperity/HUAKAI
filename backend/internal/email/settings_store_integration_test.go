//go:build integration_pg

package email

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// openEmailTestPool 打开 dev 集成库连接;未设 HUAKAI_DATABASE_URL 时跳过。
func openEmailTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestProductionGateExcludesSystemPseudoTenant_Integration 守 B0:
// 生产 email 就绪门必须排除 id<=0 系统伪租户(迁移 0030 播种的 id=0 'public-pricing' 哨兵),
// 否则该哨兵(任何配置入口都拒写)会让门永久拒启,production 起不来。
//
// 全程在一个事务内操作且**永不 Commit、defer Rollback**:对共享 dev 库零持久写入,
// 进程被硬杀时连接关闭自动回滚,既 crash-safe 又幂等(重跑不残留、不撞 tenants 名唯一索引)。
// store 接收 pgx.Tx(满足 db.DBTX),所有读写都在事务视图内进行。
//
// 判别性:AssertA 直接断言 ListActiveTenantIDs 不含 0 —— 与 email 配置无关,
// 删掉 SQL 里的 `AND id > 0` 即返回含 0(转 RED)。AssertB 端到端断言门放行,
// 删过滤后门会因哨兵未配置 SMTP 而失败(转 RED)。两条都判别。
func TestProductionGateExcludesSystemPseudoTenant_Integration(t *testing.T) {
	ctx := context.Background()
	pool := openEmailTestPool(t, ctx)
	keys := testEmailKeys(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	// 测试永不 Commit;无论成败/崩溃都回滚,共享库零残留。
	defer func() { _ = tx.Rollback(ctx) }()

	// 前置:确认哨兵 tenant 0 处于 active(迁移 0030 应已播种,事务可见);
	// 否则本测试无法真正行使排除逻辑,直接失败提示而非静默假绿。
	var sentinelActive bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tenants WHERE id = 0 AND status = 'active' AND deleted_at IS NULL)`,
	).Scan(&sentinelActive); err != nil {
		t.Fatalf("probe sentinel tenant 0: %v", err)
	}
	if !sentinelActive {
		t.Fatal("前置不满足:期望迁移 0030 播种的 tenant 0 'public-pricing' 为 active,本测试需以它行使排除逻辑")
	}
	// 事务内清掉哨兵可能残留的 email_settings(正常无;保证哨兵确为"未配置",让变异判别可靠)。
	if _, err := tx.Exec(ctx, `DELETE FROM email_settings WHERE tenant_id = 0`); err != nil {
		t.Fatalf("clear sentinel email settings: %v", err)
	}

	// 事务内隐藏既有正 id 工作租户(回滚自动恢复,无需快照/cleanup),
	// 只留我们自己种的那个,避免别的未配 SMTP 租户干扰 AssertB。
	if _, err := tx.Exec(ctx, `
		UPDATE tenants
		SET status = 'deleted', deleted_at = now()
		WHERE id > 0 AND deleted_at IS NULL
	`); err != nil {
		t.Fatalf("hide working tenants: %v", err)
	}

	// 事务内造一个正 id active 工作租户。固定名在事务回滚保证下不会跨运行残留,
	// 故不会撞 tenants 名 partial unique index。
	var workingID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO tenants (name, status) VALUES ($1, 'active') RETURNING id`, "b0-email-gate-it").Scan(&workingID); err != nil {
		t.Fatalf("seed working tenant: %v", err)
	}

	store := NewPostgresSettingsStore(tx)
	// 给正 id 工作租户配齐 SMTP(含 verify=true),用真 Save 持久化加密信封(在事务内)。
	if err := store.Save(ctx, workingID, map[string]string(completeRawSettings(t, keys, workingID)), "b0-it"); err != nil {
		t.Fatalf("save working tenant smtp: %v", err)
	}

	// AssertA(主判别):active 列表排除哨兵 0、包含正 id 工作租户。
	ids, err := store.ListActiveTenantIDs(ctx)
	if err != nil {
		t.Fatalf("ListActiveTenantIDs: %v", err)
	}
	var sawSentinel, sawWorking bool
	for _, id := range ids {
		if id == 0 {
			sawSentinel = true
		}
		if id == workingID {
			sawWorking = true
		}
	}
	if sawSentinel {
		t.Fatalf("active 列表不应含系统伪租户 0,实际=%v", ids)
	}
	if !sawWorking {
		t.Fatalf("active 列表应含正 id 工作租户 %d,实际=%v", workingID, ids)
	}

	// AssertB(端到端):哨兵被排除 + 工作租户配齐 → 生产门放行。
	if err := ValidateProductionReleaseGate(ctx, store, keys); err != nil {
		t.Fatalf("生产 email 门应放行(哨兵已排除、工作租户已配齐),实际 err=%v", err)
	}
}
