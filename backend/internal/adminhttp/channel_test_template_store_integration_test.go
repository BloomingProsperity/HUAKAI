//go:build integration_pg

package adminhttp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestChannelTestTemplateStore_LogFailureRollsBackMutation(t *testing.T) {
	ctx := context.Background()
	pool := openAdminIntegrationPool(t, ctx)
	suffix := uuid.NewString()
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants(name) VALUES($1) RETURNING id`,
		"channel-template-log-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM channel_test_templates WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	store := NewChannelTestTemplateStoreAdapter(admindb.New(pool), pool)
	audit := channelTestTemplateAudit{
		ActorID: "admin_token:template-integration", ActorRole: "platform_admin",
		RequestID: "template-integration",
	}
	baseline, err := store.CreateChannelTestTemplateWithAudit(ctx, admindb.CreateChannelTestTemplateParams{
		TenantID: tenantID, Name: "baseline", Method: "GET", Path: "/v1/models",
		BodyTemplate: "", Headers: []byte(`{}`),
	}, audit)
	if err != nil {
		t.Fatalf("create baseline template: %v", err)
	}

	identifier := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "reject_template_log_" + identifier
	triggerName := "reject_template_log_trigger_" + identifier
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'template log rejected for atomicity test';
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

	if _, err := store.CreateChannelTestTemplateWithAudit(ctx, admindb.CreateChannelTestTemplateParams{
		TenantID: tenantID, Name: "must-not-exist", Method: "GET", Path: "/v1/models",
		BodyTemplate: "", Headers: []byte(`{}`),
	}, audit); err == nil {
		t.Fatal("日志失败时新增模板必须失败")
	}
	var count int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM channel_test_templates WHERE tenant_id=$1 AND name='must-not-exist'`,
		tenantID,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("新增日志失败留下模板 count=%d err=%v", count, err)
	}

	if _, err := store.UpdateChannelTestTemplateWithAudit(ctx, admindb.UpdateChannelTestTemplateParams{
		TenantID: tenantID, ID: baseline.ID, Name: "must-not-stick",
		Method: "POST", Path: "/v1/chat/completions", BodyTemplate: `{}`, Headers: []byte(`{}`),
	}, audit); err == nil {
		t.Fatal("日志失败时更新模板必须失败")
	}
	var name string
	if err := pool.QueryRow(ctx,
		`SELECT name FROM channel_test_templates WHERE tenant_id=$1 AND id=$2`,
		tenantID, baseline.ID,
	).Scan(&name); err != nil || name != "baseline" {
		t.Fatalf("更新日志失败留下半状态 name=%q err=%v", name, err)
	}

	if _, err := store.DeleteChannelTestTemplateWithAudit(ctx, admindb.DeleteChannelTestTemplateParams{
		TenantID: tenantID, ID: baseline.ID,
	}, audit); err == nil {
		t.Fatal("日志失败时删除模板必须失败")
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM channel_test_templates WHERE tenant_id=$1 AND id=$2`,
		tenantID, baseline.ID,
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("删除日志失败后模板 count=%d err=%v", count, err)
	}
}
