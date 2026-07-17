//go:build integration_pg

package adminhttp

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestProviderAccountBulkAdapter_AuditFailureRollsBackAccount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAdminIntegrationPool(t, ctx)
	tenantID, accountID := seedProviderAccountBulkTarget(t, ctx, pool, "batch-audit")
	rejectActor := "bulk-reject-" + uuid.NewString()
	installProviderAccountBulkAuditRejectTrigger(t, ctx, pool, rejectActor)

	adapter := NewProviderAccountBulkStoreAdapter(admindb.New(pool), pool)
	disabled := false
	_, err := adapter.UpdateProviderAccountByTagWithAudit(ctx, providerAccountBulkItemParams{
		TenantID:  tenantID,
		ID:        accountID,
		Tag:       "batch-audit",
		ActorID:   rejectActor,
		ActorRole: admin.RoleTenantOperator,
		Enabled:   &disabled,
	})
	if err == nil {
		t.Fatal("审计触发器拒绝时事务必须失败")
	}

	var enabled bool
	if err := pool.QueryRow(ctx,
		`SELECT enabled FROM provider_accounts WHERE tenant_id=$1 AND id=$2`,
		tenantID, accountID,
	).Scan(&enabled); err != nil {
		t.Fatalf("读取账号状态: %v", err)
	}
	if !enabled {
		t.Fatal("审计失败后 enabled 必须保持 true，账号修改应随事务回滚")
	}

	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM admin_audit_events WHERE tenant_id=$1 AND target_id=$2`,
		tenantID, accountID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("读取审计数量: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("审计失败后不应留下审计行，实际=%d", auditCount)
	}
}

func TestProviderAccountBulkAdapter_CommitsUpdateAndLegalAudit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAdminIntegrationPool(t, ctx)
	tenantID, accountID := seedProviderAccountBulkTarget(t, ctx, pool, "batch-happy")

	adapter := NewProviderAccountBulkStoreAdapter(admindb.New(pool), pool)
	priority := int32(9)
	outcome, err := adapter.UpdateProviderAccountByTagWithAudit(ctx, providerAccountBulkItemParams{
		TenantID:  tenantID,
		ID:        accountID,
		Tag:       "batch-happy",
		ActorID:   "bulk-happy-" + uuid.NewString(),
		ActorRole: admin.RoleTenantOperator,
		Priority:  &priority,
	})
	if err != nil {
		t.Fatalf("正常批量单项事务失败: %v", err)
	}
	if outcome.Status != "succeeded" {
		t.Fatalf("outcome=%+v want succeeded", outcome)
	}

	var gotPriority int32
	if err := pool.QueryRow(ctx,
		`SELECT priority FROM provider_accounts WHERE tenant_id=$1 AND id=$2`,
		tenantID, accountID,
	).Scan(&gotPriority); err != nil {
		t.Fatalf("读取账号优先级: %v", err)
	}
	if gotPriority != priority {
		t.Fatalf("priority=%d want %d", gotPriority, priority)
	}

	var action string
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT min(action), count(*)
		FROM admin_audit_events
		WHERE tenant_id=$1 AND target_type='provider_account' AND target_id=$2
	`, tenantID, accountID).Scan(&action, &auditCount); err != nil {
		t.Fatalf("读取账号审计: %v", err)
	}
	if auditCount != 1 || action != "update_provider_account" {
		t.Fatalf("audit count=%d action=%q want one legal update_provider_account", auditCount, action)
	}
}

func TestProviderAccountBulkAdapter_NoLongerMatchingTagIsSkipped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAdminIntegrationPool(t, ctx)
	tenantID, accountID := seedProviderAccountBulkTarget(t, ctx, pool, "other-tag")

	adapter := NewProviderAccountBulkStoreAdapter(admindb.New(pool), pool)
	disabled := false
	outcome, err := adapter.UpdateProviderAccountByTagWithAudit(ctx, providerAccountBulkItemParams{
		TenantID:  tenantID,
		ID:        accountID,
		Tag:       "batch-target",
		ActorID:   "bulk-skip-" + uuid.NewString(),
		ActorRole: admin.RoleTenantOperator,
		Enabled:   &disabled,
	})
	if err != nil {
		t.Fatalf("标签已漂移应跳过而非失败: %v", err)
	}
	if outcome.Status != "skipped" || outcome.Code != "target_no_longer_matches" {
		t.Fatalf("outcome=%+v want target_no_longer_matches", outcome)
	}

	var enabled bool
	if err := pool.QueryRow(ctx,
		`SELECT enabled FROM provider_accounts WHERE tenant_id=$1 AND id=$2`,
		tenantID, accountID,
	).Scan(&enabled); err != nil {
		t.Fatalf("读取账号状态: %v", err)
	}
	if !enabled {
		t.Fatal("标签已不匹配时账号不得被修改")
	}
}

func TestProviderAccountBulkHandler_RealPGItemFailureDoesNotBlockNextAccount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAdminIntegrationPool(t, ctx)
	tenantID, firstAccountID := seedProviderAccountBulkTarget(t, ctx, pool, "batch-partial")
	secondAccountID := seedAdditionalProviderAccountBulkTarget(t, ctx, pool, tenantID, firstAccountID, "batch-partial")
	installProviderAccountBulkTargetAuditRejectTrigger(t, ctx, pool, firstAccountID)

	deps := ProviderAccountBulkDeps{
		Auth: &stubBulkAuth{ident: admin.AdminIdentity{
			Role:          admin.RoleTenantOperator,
			ScopeTenantID: tenantID,
			TokenID:       7001,
		}},
		Store: NewProviderAccountBulkStoreAdapter(admindb.New(pool), pool),
	}
	rec := doProviderAccountBulkPOST(t, deps, map[string]any{
		"tag":     "batch-partial",
		"enabled": false,
	}, tenantID)
	if rec.Code != 207 {
		t.Fatalf("status=%d body=%s want 207", rec.Code, rec.Body.String())
	}
	var resp providerAccountBulkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if resp.Total != 2 || resp.Succeeded != 1 || resp.Failed != 1 || resp.Skipped != 0 {
		t.Fatalf("summary=%+v want one failed and one succeeded", resp)
	}

	var firstEnabled, secondEnabled bool
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT enabled FROM provider_accounts WHERE tenant_id=$1 AND id=$2),
			(SELECT enabled FROM provider_accounts WHERE tenant_id=$1 AND id=$3)
	`, tenantID, firstAccountID, secondAccountID).Scan(&firstEnabled, &secondEnabled); err != nil {
		t.Fatalf("读取两个账号状态: %v", err)
	}
	if !firstEnabled {
		t.Fatal("第一条审计失败时对应账号必须回滚")
	}
	if secondEnabled {
		t.Fatal("第一条失败不得阻断第二条账号成功提交")
	}

	var firstAuditCount, secondAuditCount int
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE target_id=$2),
			count(*) FILTER (WHERE target_id=$3)
		FROM admin_audit_events
		WHERE tenant_id=$1 AND target_type='provider_account'
	`, tenantID, firstAccountID, secondAccountID).Scan(&firstAuditCount, &secondAuditCount); err != nil {
		t.Fatalf("读取两个账号审计数量: %v", err)
	}
	if firstAuditCount != 0 || secondAuditCount != 1 {
		t.Fatalf("audit counts first=%d second=%d want 0/1", firstAuditCount, secondAuditCount)
	}
}

func seedProviderAccountBulkTarget(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tag string) (int64, int64) {
	t.Helper()
	suffix := uuid.NewString()[:8]
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"bulk-account-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("创建测试租户: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	var providerID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		VALUES ($1,$2,$3,'openai_chat')
		RETURNING id
	`, tenantID, "bulk-"+suffix, "Bulk "+suffix).Scan(&providerID); err != nil {
		t.Fatalf("创建测试 provider: %v", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`,
		tenantID, "bulk-pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("创建测试 pool: %v", err)
	}
	var channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`,
		tenantID, poolGroupID, "bulk-channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("创建测试 channel: %v", err)
	}
	var accountID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO provider_accounts
		    (tenant_id, provider_id, channel_id, name, account_type, credentials, tags, extra, enabled, priority, static_weight)
		VALUES ($1,$2,$3,$4,'api_key','{}',ARRAY[$5::text],'{}',true,1,1)
		RETURNING id
	`, tenantID, providerID, channelID, "bulk-account-"+suffix, tag).Scan(&accountID); err != nil {
		t.Fatalf("创建测试账号: %v", err)
	}
	return tenantID, accountID
}

func seedAdditionalProviderAccountBulkTarget(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, sourceAccountID int64, tag string) int64 {
	t.Helper()
	var accountID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO provider_accounts
		    (tenant_id, provider_id, channel_id, name, account_type, credentials, tags, extra, enabled, priority, static_weight)
		SELECT tenant_id, provider_id, channel_id, $3, account_type, '{}', ARRAY[$4::text], '{}', true, 1, 1
		FROM provider_accounts
		WHERE tenant_id=$1 AND id=$2
		RETURNING id
	`, tenantID, sourceAccountID, "bulk-account-"+uuid.NewString()[:8], tag).Scan(&accountID); err != nil {
		t.Fatalf("创建第二个测试账号: %v", err)
	}
	return accountID
}

func installProviderAccountBulkAuditRejectTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rejectActor string) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "audit_reject_bulk_" + suffix
	triggerName := "trg_audit_reject_bulk_" + suffix
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION `+functionName+`() RETURNS trigger AS $$
		BEGIN
			IF NEW.actor_id = '`+rejectActor+`' THEN
				RAISE EXCEPTION 'provider account bulk audit rejected';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql
	`); err != nil {
		t.Fatalf("创建审计拒绝函数: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TRIGGER `+triggerName+`
		BEFORE INSERT ON admin_audit_events
		FOR EACH ROW EXECUTE FUNCTION `+functionName+`()
	`); err != nil {
		t.Fatalf("创建审计拒绝触发器: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DROP TRIGGER IF EXISTS `+triggerName+` ON admin_audit_events`)
		_, _ = pool.Exec(bg, `DROP FUNCTION IF EXISTS `+functionName+`()`)
	})
}

func installProviderAccountBulkTargetAuditRejectTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rejectTargetID int64) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "audit_reject_bulk_target_" + suffix
	triggerName := "trg_audit_reject_bulk_target_" + suffix
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION `+functionName+`() RETURNS trigger AS $$
		BEGIN
			IF NEW.target_id = `+strconv.FormatInt(rejectTargetID, 10)+` THEN
				RAISE EXCEPTION 'provider account bulk target audit rejected';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql
	`); err != nil {
		t.Fatalf("创建目标审计拒绝函数: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TRIGGER `+triggerName+`
		BEFORE INSERT ON admin_audit_events
		FOR EACH ROW EXECUTE FUNCTION `+functionName+`()
	`); err != nil {
		t.Fatalf("创建目标审计拒绝触发器: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DROP TRIGGER IF EXISTS `+triggerName+` ON admin_audit_events`)
		_, _ = pool.Exec(bg, `DROP FUNCTION IF EXISTS `+functionName+`()`)
	})
}
