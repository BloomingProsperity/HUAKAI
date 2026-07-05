//go:build integration_pg

package notify

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

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
