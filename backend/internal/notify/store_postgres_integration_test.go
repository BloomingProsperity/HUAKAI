//go:build integration_pg

package notify

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestPostgresListActiveSettingsOnlyReturnsAdminsForBroadcast(t *testing.T) {
	ctx := context.Background()
	pool := notifyTestPool(t)
	keys, err := credentialstore.NewStaticKeyProvider("notify-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("new key provider: %v", err)
	}
	store := NewPostgresStore(pool, keys)
	tenantID := notifyInsertTenant(t, ctx, pool, "notify-broadcast-"+fmt.Sprint(time.Now().UnixNano()))
	adminUserID := notifyInsertUser(t, ctx, pool, tenantID, "admin")
	normalUserID := notifyInsertUser(t, ctx, pool, tenantID, "user")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_notification_settings WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})

	for _, userID := range []int64{adminUserID, normalUserID} {
		if _, err := store.UpsertSettings(ctx, Settings{
			TenantID:         tenantID,
			UserID:           userID,
			NotifyType:       TypeWebhook,
			WebhookURL:       fmt.Sprintf("https://hooks.example.test/%d", userID),
			WebhookSecret:    "secret",
			BalanceThreshold: decimal.RequireFromString("10.00000000"),
		}); err != nil {
			t.Fatalf("upsert settings user=%d: %v", userID, err)
		}
	}

	active, err := store.ListActiveSettings(ctx, tenantID)
	if err != nil {
		t.Fatalf("ListActiveSettings: %v", err)
	}
	if len(active) != 1 || active[0].UserID != adminUserID {
		t.Fatalf("broadcast active settings=%+v, want only admin user %d", active, adminUserID)
	}
	normalSettings, err := store.GetSettings(ctx, tenantID, normalUserID)
	if err != nil {
		t.Fatalf("GetSettings normal user: %v", err)
	}
	if normalSettings.NotifyType != TypeWebhook || normalSettings.WebhookURL == "" {
		t.Fatalf("GetSettings 普通用户单发路径被误伤: %+v", normalSettings)
	}
}

func TestAdminNotificationSettingsAndSecurityLogCommitAtomically(t *testing.T) {
	ctx := context.Background()
	pool := notifyTestPool(t)
	keys, err := credentialstore.NewStaticKeyProvider("notify-admin-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("new key provider: %v", err)
	}
	store := NewPostgresStore(pool, keys)
	suffix := fmt.Sprint(time.Now().UnixNano())
	tenantID := notifyInsertTenant(t, ctx, pool, "notify-admin-"+suffix)
	userID := notifyInsertUser(t, ctx, pool, tenantID, "user")
	requestID := "req-notify-admin-" + suffix
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_audit_events WHERE request_id = $1`, requestID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_notification_settings WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})

	saved, err := store.UpsertSettingsWithAdminLog(ctx, Settings{
		TenantID:         tenantID,
		UserID:           userID,
		NotifyType:       TypeWebhook,
		WebhookURL:       "https://hooks.example.test/admin",
		WebhookSecret:    "plain-secret-must-not-log",
		ThresholdType:    "percentage",
		ExtraEmails:      []string{"ops@example.test"},
		BalanceThreshold: decimal.RequireFromString("10.00000000"),
	}, AdminMutation{
		Actor:     "admin_user:88",
		ActorRole: "tenant_operator",
		RequestID: requestID,
	})
	if err != nil {
		t.Fatalf("UpsertSettingsWithAdminLog: %v", err)
	}
	if saved.UpdatedBy != "admin_user:88" || saved.NotifyType != TypeWebhook {
		t.Fatalf("saved=%+v want attributed webhook settings", saved)
	}

	var (
		action     string
		targetType string
		targetID   int64
		category   string
		payload    string
	)
	if err := pool.QueryRow(ctx, `
SELECT action, target_type, target_id, log_category, payload::text
FROM admin_audit_events
WHERE request_id = $1
`, requestID).Scan(&action, &targetType, &targetID, &category, &payload); err != nil {
		t.Fatalf("read security log: %v", err)
	}
	if action != "update_user_notification_settings" || targetType != "user_notification_settings" ||
		targetID != userID || category != "security" {
		t.Fatalf("log action=%q target=%q/%d category=%q", action, targetType, targetID, category)
	}
	if strings.Contains(payload, "plain-secret-must-not-log") || !strings.Contains(payload, `"webhook_configured": true`) {
		t.Fatalf("安全日志 payload=%s，不能包含秘密且必须记录配置结果", payload)
	}
}

func TestAdminNotificationLogFailureRollsBackSettings(t *testing.T) {
	ctx := context.Background()
	pool := notifyTestPool(t)
	keys, err := credentialstore.NewStaticKeyProvider("notify-admin-rollback", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("new key provider: %v", err)
	}
	store := NewPostgresStore(pool, keys)
	suffix := fmt.Sprint(time.Now().UnixNano())
	tenantID := notifyInsertTenant(t, ctx, pool, "notify-rollback-"+suffix)
	userID := notifyInsertUser(t, ctx, pool, tenantID, "user")
	if _, err := store.UpsertSettings(ctx, Settings{
		TenantID:   tenantID,
		UserID:     userID,
		NotifyType: TypeNone,
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	requestID := "req-notify-rollback-" + suffix
	functionName := pgx.Identifier{"huakai_fail_notify_log_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"huakai_fail_notify_log_trigger_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'forced notification settings log failure';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER %s
BEFORE INSERT ON admin_audit_events
FOR EACH ROW
WHEN (NEW.request_id = '%s')
EXECUTE FUNCTION %s();
`, functionName, triggerName, requestID, functionName)); err != nil {
		t.Fatalf("install log failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON admin_audit_events; DROP FUNCTION IF EXISTS %s();",
			triggerName, functionName,
		))
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_audit_events WHERE request_id = $1`, requestID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_notification_settings WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})

	_, err = store.UpsertSettingsWithAdminLog(ctx, Settings{
		TenantID:          tenantID,
		UserID:            userID,
		NotifyType:        TypeEmail,
		NotificationEmail: "changed@example.test",
	}, AdminMutation{
		Actor:     "admin_user:99",
		ActorRole: "tenant_operator",
		RequestID: requestID,
	})
	if err == nil || !strings.Contains(err.Error(), "insert notification settings log") {
		t.Fatalf("err=%v want security-log failure", err)
	}
	current, err := store.GetSettings(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("GetSettings after rollback: %v", err)
	}
	if current.NotifyType != TypeNone || current.NotificationEmail != "" {
		t.Fatalf("日志失败后 settings=%+v want original none state", current)
	}
}

func notifyTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func notifyInsertTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	return id
}

func notifyInsertUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, role string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (tenant_id, display_name, role)
		VALUES ($1, $2, $3)
		RETURNING id`, tenantID, "notify-"+role, role).Scan(&id); err != nil {
		t.Fatalf("insert %s user: %v", role, err)
	}
	return id
}
