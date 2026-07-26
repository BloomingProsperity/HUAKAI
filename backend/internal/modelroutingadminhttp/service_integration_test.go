//go:build integration_pg

package modelroutingadminhttp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	dbmodelroutingadmin "github.com/BloomingProsperity/HUAKAI/internal/db/modelroutingadmin"
)

type routingOverrideSeed struct {
	tenantID           int64
	otherTenantID      int64
	poolGroupID        int64
	otherPoolGroupID   int64
	providerAccountID  int64
	alternateAccountID int64
	otherAccountID     int64
}

func routingIntegrationAudit() MutationAudit {
	return MutationAudit{
		ActorID: "admin_token:routing-integration", ActorRole: "platform_admin",
		RequestID: "routing-integration",
	}
}

func openRoutingOverrideIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 PostgreSQL：%v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedRoutingOverrideGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool) routingOverrideSeed {
	t.Helper()
	unique := uuid.NewString()
	var seed routingOverrideSeed
	for _, target := range []struct {
		tenant  *int64
		pool    *int64
		account *int64
		label   string
	}{
		{&seed.tenantID, &seed.poolGroupID, &seed.providerAccountID, "a"},
		{&seed.otherTenantID, &seed.otherPoolGroupID, &seed.otherAccountID, "b"},
	} {
		if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "routing-override-"+target.label+"-"+unique).Scan(target.tenant); err != nil {
			t.Fatalf("创建租户 %s：%v", target.label, err)
		}
		var providerID, channelID int64
		if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1,$2,$3,'openai_chat') RETURNING id`,
			*target.tenant, "provider-"+target.label+"-"+unique, "Provider "+target.label).Scan(&providerID); err != nil {
			t.Fatalf("创建 provider %s：%v", target.label, err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`,
			*target.tenant, "pool-"+target.label+"-"+unique).Scan(target.pool); err != nil {
			t.Fatalf("创建 pool %s：%v", target.label, err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`,
			*target.tenant, *target.pool, "channel-"+target.label+"-"+unique).Scan(&channelID); err != nil {
			t.Fatalf("创建 channel %s：%v", target.label, err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type) VALUES ($1,$2,$3,$4,'api_key') RETURNING id`,
			*target.tenant, providerID, channelID, "account-"+target.label+"-"+unique).Scan(target.account); err != nil {
			t.Fatalf("创建 account %s：%v", target.label, err)
		}
		if target.label == "a" {
			if err := pool.QueryRow(ctx, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type) VALUES ($1,$2,$3,$4,'api_key') RETURNING id`,
				*target.tenant, providerID, channelID, "account-a-alt-"+unique).Scan(&seed.alternateAccountID); err != nil {
				t.Fatalf("创建同池备用 account：%v", err)
			}
		}
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, tenantID := range []int64{seed.tenantID, seed.otherTenantID} {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM model_routing_overrides WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, tenantID)
		}
	})
	return seed
}

// 本用例是写口到既有消费查询的闭环证据。
// 变异：INSERT 漏掉 provider_account_ids、写错 pool/model/tenant 任一列，精确读回都会转红。
func TestPostgresService_CRUDFeedsGetModelRoutingForGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRoutingOverrideIntegrationPool(t, ctx)
	seed := seedRoutingOverrideGraph(t, ctx, pool)
	queries := dbbilling.New(pool)
	service := NewPostgresService(pool, dbmodelroutingadmin.New(pool))

	created, err := service.Create(ctx, CreateInput{
		TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID, Model: "gpt-pin",
		ProviderAccountIDs: []int64{seed.providerAccountID}, Enabled: true,
		Audit: routingIntegrationAudit(),
	})
	if err != nil {
		t.Fatalf("创建 override：%v", err)
	}
	rows, err := queries.GetModelRoutingForGroup(ctx, dbbilling.GetModelRoutingForGroupParams{
		TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID, Model: "gpt-pin",
	})
	if err != nil {
		t.Fatalf("消费查询：%v", err)
	}
	if len(rows) != 1 || len(rows[0].ProviderAccountIds) != 1 || rows[0].ProviderAccountIds[0] != seed.providerAccountID {
		t.Fatalf("消费查询未精确读回账号子集：%+v", rows)
	}

	replacement := []int64{seed.alternateAccountID}
	updated, err := service.Update(ctx, UpdateInput{ID: created.ID, TenantID: seed.tenantID, ProviderAccountIDs: &replacement, Audit: routingIntegrationAudit()})
	if err != nil || len(updated.ProviderAccountIDs) != 1 || updated.ProviderAccountIDs[0] != seed.alternateAccountID {
		t.Fatalf("更新账号子集：row=%+v err=%v", updated, err)
	}
	rows, err = queries.GetModelRoutingForGroup(ctx, dbbilling.GetModelRoutingForGroupParams{
		TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID, Model: "gpt-pin",
	})
	if err != nil || len(rows) != 1 || len(rows[0].ProviderAccountIds) != 1 || rows[0].ProviderAccountIds[0] != seed.alternateAccountID {
		t.Fatalf("更新后消费查询未读回新账号子集：rows=%+v err=%v", rows, err)
	}

	disabled := false
	updated, err = service.Update(ctx, UpdateInput{ID: created.ID, TenantID: seed.tenantID, Enabled: &disabled, Audit: routingIntegrationAudit()})
	if err != nil || updated.Enabled {
		t.Fatalf("禁用 override：row=%+v err=%v", updated, err)
	}
	rows, err = queries.GetModelRoutingForGroup(ctx, dbbilling.GetModelRoutingForGroupParams{
		TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID, Model: "gpt-pin",
	})
	if err != nil || len(rows) != 0 {
		t.Fatalf("禁用后消费查询仍命中：rows=%+v err=%v", rows, err)
	}
	if err := service.Delete(ctx, created.ID, seed.tenantID, routingIntegrationAudit()); err != nil {
		t.Fatalf("删除 override：%v", err)
	}
	items, err := service.List(ctx, seed.tenantID)
	if err != nil || len(items) != 0 {
		t.Fatalf("软删后列表仍命中：items=%+v err=%v", items, err)
	}
	recreated, err := service.Create(ctx, CreateInput{
		TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID, Model: "gpt-pin",
		ProviderAccountIDs: []int64{seed.providerAccountID}, Enabled: true,
		Audit: routingIntegrationAudit(),
	})
	if err != nil || recreated.ID == created.ID {
		t.Fatalf("软删后应可按同一池和模型重建：row=%+v err=%v", recreated, err)
	}
}

// pool_group 必须由目标租户拥有；删除 pool 校验会让本用例创建成功并转红。
func TestPostgresService_RejectsCrossTenantPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRoutingOverrideIntegrationPool(t, ctx)
	seed := seedRoutingOverrideGraph(t, ctx, pool)
	service := NewPostgresService(pool, dbmodelroutingadmin.New(pool))

	_, err := service.Create(ctx, CreateInput{
		TenantID: seed.tenantID, PoolGroupID: seed.otherPoolGroupID, Model: "gpt-pin",
		ProviderAccountIDs: []int64{seed.providerAccountID}, Enabled: true,
		Audit: routingIntegrationAudit(),
	})
	if !errors.Is(err, ErrPoolNotOwned) {
		t.Fatalf("错误=%v，期望 ErrPoolNotOwned", err)
	}
}

// 数组中任一账号不属于目标租户/池都必须整体拒绝，不能静默丢掉非法 ID 后部分写入。
// 变异：删除账号归属校验会让 INSERT 成功并转红。
func TestPostgresService_RejectsCrossTenantAccount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRoutingOverrideIntegrationPool(t, ctx)
	seed := seedRoutingOverrideGraph(t, ctx, pool)
	service := NewPostgresService(pool, dbmodelroutingadmin.New(pool))

	_, err := service.Create(ctx, CreateInput{
		TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID, Model: "gpt-pin",
		ProviderAccountIDs: []int64{seed.providerAccountID, seed.otherAccountID}, Enabled: true,
		Audit: routingIntegrationAudit(),
	})
	if !errors.Is(err, ErrAccountsNotOwned) {
		t.Fatalf("错误=%v，期望 ErrAccountsNotOwned", err)
	}
	items, listErr := service.List(ctx, seed.tenantID)
	if listErr != nil || len(items) != 0 {
		t.Fatalf("非法账号不应部分落库：items=%+v err=%v", items, listErr)
	}
}

// 操作日志失败必须让强制 pin 的新增、更新和删除一并回滚。
func TestPostgresService_LogFailureRollsBackOverrideMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRoutingOverrideIntegrationPool(t, ctx)
	seed := seedRoutingOverrideGraph(t, ctx, pool)
	service := NewPostgresService(pool, dbmodelroutingadmin.New(pool))
	baseline, err := service.Create(ctx, CreateInput{
		TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID, Model: "gpt-log-baseline",
		ProviderAccountIDs: []int64{seed.providerAccountID}, Enabled: true,
		Audit: routingIntegrationAudit(),
	})
	if err != nil {
		t.Fatalf("create baseline override: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "reject_routing_log_" + suffix
	triggerName := "reject_routing_log_trigger_" + suffix
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'routing log rejected for atomicity test';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER %s BEFORE INSERT ON admin_audit_events
FOR EACH ROW EXECUTE FUNCTION %s()`, functionName, triggerName, functionName)); err != nil {
		t.Fatalf("install reject trigger: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON admin_audit_events`, triggerName))
		_, _ = pool.Exec(c, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})

	if _, err := service.Create(ctx, CreateInput{
		TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID, Model: "gpt-log-rejected",
		ProviderAccountIDs: []int64{seed.providerAccountID}, Enabled: true,
		Audit: routingIntegrationAudit(),
	}); err == nil {
		t.Fatal("日志失败时新增强制 pin 必须失败")
	}
	var count int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM model_routing_overrides WHERE tenant_id=$1 AND model='gpt-log-rejected' AND deleted_at IS NULL`,
		seed.tenantID,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("新增日志失败留下强制 pin count=%d err=%v", count, err)
	}

	disabled := false
	if _, err := service.Update(ctx, UpdateInput{
		ID: baseline.ID, TenantID: seed.tenantID, Enabled: &disabled, Audit: routingIntegrationAudit(),
	}); err == nil {
		t.Fatal("日志失败时更新强制 pin 必须失败")
	}
	var enabled bool
	if err := pool.QueryRow(ctx,
		`SELECT enabled FROM model_routing_overrides WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, baseline.ID,
	).Scan(&enabled); err != nil || !enabled {
		t.Fatalf("更新日志失败留下半状态 enabled=%v err=%v", enabled, err)
	}

	if err := service.Delete(ctx, baseline.ID, seed.tenantID, routingIntegrationAudit()); err == nil {
		t.Fatal("日志失败时删除强制 pin 必须失败")
	}
	var deletedAt any
	if err := pool.QueryRow(ctx,
		`SELECT deleted_at FROM model_routing_overrides WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, baseline.ID,
	).Scan(&deletedAt); err != nil || deletedAt != nil {
		t.Fatalf("删除日志失败留下半状态 deleted_at=%v err=%v", deletedAt, err)
	}
}
