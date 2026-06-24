//go:build integration_pg

package gatewayhttp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

func openAdminPoolsTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// seedAdminPoolsTenant 种一个 tenant 行，返回 ID 并注册 cleanup。
func seedAdminPoolsTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) int64 {
	t.Helper()
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-pools-tx-tenant-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return tenantID
}

// installAdminAuditRejectTrigger 装一个 BEFORE INSERT trigger，拒收
// actor_id = rejectActorID 的 admin_audit_events 行。
func installAdminAuditRejectTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, rejectActorID string) {
	t.Helper()
	fnName := "audit_reject_" + name
	trigName := "trg_" + name
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION `+fnName+`() RETURNS trigger AS $$
		BEGIN
			IF NEW.actor_id = '`+rejectActorID+`' THEN
				RAISE EXCEPTION 'admin_pools_tx test reject actor_id %', NEW.actor_id;
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create reject fn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DROP TRIGGER IF EXISTS `+trigName+` ON admin_audit_events;
		CREATE TRIGGER `+trigName+` BEFORE INSERT ON admin_audit_events
		FOR EACH ROW EXECUTE FUNCTION `+fnName+`()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DROP TRIGGER IF EXISTS `+trigName+` ON admin_audit_events`)
		_, _ = pool.Exec(c, `DROP FUNCTION IF EXISTS `+fnName+`()`)
	})
}

// TestAdminPoolsCreate_AuditFailureRollsBackPool 验证 create pool 与 admin
// audit insert 同事务：trigger 拒绝 audit row 时，pool_groups 不应留下行。
//
// Mutation 自检：若 adapter 先 InsertPool 提交、再单独 InsertAdminAuditEvent，
// audit 拒绝后 pool 行会留下，本用例 red。
func TestAdminPoolsCreate_AuditFailureRollsBackPool(t *testing.T) {
	ctx := context.Background()
	pool := openAdminPoolsTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID := seedAdminPoolsTenant(t, ctx, pool, suffix)
	rejectActor := "gw10-create-" + suffix
	installAdminAuditRejectTrigger(t, ctx, pool, "create_"+strings.ReplaceAll(suffix, "-", "_"), rejectActor)

	adapter := NewAdminPoolsStoreAdapter(dbbilling.New(pool), admindb.New(pool), pool)
	poolName := "tx-create-" + suffix
	requestID := "req-" + suffix

	_, err := adapter.CreatePoolWithAudit(ctx,
		dbbilling.InsertPoolParams{
			TenantID:          tenantID,
			Name:              poolName,
			TopKDefault:       1,
			CapabilityDefault: "exact_capability_only",
			AllowLastResort:   false,
		},
		admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantID,
			ActorID:    rejectActor, // trigger 拒绝
			ActorRole:  admin.RolePlatformAdmin,
			Action:     "create_pool_group",
			TargetType: "pool_group",
			RequestID:  &requestID,
			Payload:    []byte(`{"name":"` + poolName + `"}`),
		},
	)
	if err == nil {
		t.Fatalf("CreatePoolWithAudit must fail when audit trigger rejects; got nil err")
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pool_groups WHERE tenant_id=$1 AND name=$2`,
		tenantID, poolName,
	).Scan(&count); err != nil {
		t.Fatalf("count pool_groups: %v", err)
	}
	if count != 0 {
		t.Fatalf("pool row MUST NOT be committed when audit insert fails; got %d rows", count)
	}
}

// TestAdminPoolsUpdate_AuditFailureRollsBackPool 验证 update pool 与 admin
// audit insert 同事务：audit trigger 拒绝时，pool 字段必须保持原值。
//
// Mutation 自检：若 UpdatePoolWithAudit 退化成两段非同事务，update 会先提交，
// audit 再失败，top_k_default 会被改掉，本用例 red。
func TestAdminPoolsUpdate_AuditFailureRollsBackPool(t *testing.T) {
	ctx := context.Background()
	pool := openAdminPoolsTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID := seedAdminPoolsTenant(t, ctx, pool, suffix)
	rejectActor := "gw10-update-" + suffix
	installAdminAuditRejectTrigger(t, ctx, pool, "update_"+strings.ReplaceAll(suffix, "-", "_"), rejectActor)

	adapter := NewAdminPoolsStoreAdapter(dbbilling.New(pool), admindb.New(pool), pool)
	poolName := "tx-update-baseline-" + suffix

	seeded, err := dbbilling.New(pool).InsertPool(ctx, dbbilling.InsertPoolParams{
		TenantID:          tenantID,
		Name:              poolName,
		TopKDefault:       1,
		CapabilityDefault: "exact_capability_only",
		AllowLastResort:   false,
	})
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}

	newTopK := int32(5)
	requestID := "req-upd-" + suffix
	_, err = adapter.UpdatePoolWithAudit(ctx,
		dbbilling.UpdatePoolParams{
			TopKDefault: &newTopK,
			TenantID:    tenantID,
			ID:          seeded.ID,
		},
		admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantID,
			ActorID:    rejectActor,
			ActorRole:  admin.RolePlatformAdmin,
			Action:     "update_pool_group",
			TargetType: "pool_group",
			RequestID:  &requestID,
			Payload:    []byte(`{"updated":true}`),
		},
	)
	if err == nil {
		t.Fatalf("UpdatePoolWithAudit must fail when audit trigger rejects")
	}

	var topK int32
	if err := pool.QueryRow(ctx,
		`SELECT top_k_default FROM pool_groups WHERE id=$1 AND tenant_id=$2`,
		seeded.ID, tenantID,
	).Scan(&topK); err != nil {
		t.Fatalf("read pool top_k: %v", err)
	}
	if topK != 1 {
		t.Fatalf("pool top_k MUST remain 1 when audit fails; got %d", topK)
	}
}

// TestAdminPoolsCreate_HappyPathCommitsBoth 是正向守卫：audit 成功时 pool
// 与 audit row 都必须落库。Mutation 自检：把 BeginFunc 改成只 Rollback 时，
// 本用例会 red。
func TestAdminPoolsCreate_HappyPathCommitsBoth(t *testing.T) {
	ctx := context.Background()
	pool := openAdminPoolsTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID := seedAdminPoolsTenant(t, ctx, pool, suffix)

	adapter := NewAdminPoolsStoreAdapter(dbbilling.New(pool), admindb.New(pool), pool)
	poolName := "tx-happy-" + suffix
	requestID := "req-happy-" + suffix

	pg, err := adapter.CreatePoolWithAudit(ctx,
		dbbilling.InsertPoolParams{
			TenantID:          tenantID,
			Name:              poolName,
			TopKDefault:       2,
			CapabilityDefault: "exact_capability_only",
			AllowLastResort:   false,
		},
		admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantID,
			ActorID:    "happy-" + suffix,
			ActorRole:  admin.RolePlatformAdmin,
			Action:     "create_pool_group",
			TargetType: "pool_group",
			RequestID:  &requestID,
			Payload:    []byte(`{"name":"` + poolName + `"}`),
		},
	)
	if err != nil {
		t.Fatalf("happy path CreatePoolWithAudit: %v", err)
	}
	if pg.ID == 0 || pg.Name != poolName {
		t.Fatalf("returned pool drift: %+v", pg)
	}

	var poolCount, auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pool_groups WHERE id=$1`, pg.ID).Scan(&poolCount); err != nil {
		t.Fatalf("count pool: %v", err)
	}
	if poolCount != 1 {
		t.Fatalf("happy path pool row missing")
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM admin_audit_events WHERE actor_id=$1 AND target_id=$2`,
		"happy-"+suffix, pg.ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("happy path audit row missing; got %d", auditCount)
	}
}
