//go:build integration_pg

package moderation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
)

func TestListModerationLog_TenantScopedPaginated(t *testing.T) {
	// 变异:把 tenant 谓词从 ListModerationLog 里去掉；租户 B
	// 最新的那行会占据第 1 页,本用例就会看到错误的 tenant/api key。
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	a := seedModerationAPIKey(t, ctx, pool, "logs-a", "active")
	aSecond := seedModerationAPIKeyInTenant(t, ctx, pool, a.tenantID, "logs-a-second", "active")
	b := seedModerationAPIKey(t, ctx, pool, "logs-b", "active")

	insertModerationLogAt(t, ctx, pool, a, DecisionBlockKeyword, "a-old", "hash-a-old", time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC))
	insertModerationLogAt(t, ctx, pool, aSecond, DecisionBlockHash, "a-new", "hash-a-new", time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC))
	insertModerationLogAt(t, ctx, pool, b, DecisionBlockKeyword, "b-newest", "hash-b-newest", time.Date(2026, 6, 6, 13, 0, 0, 0, time.UTC))

	got, err := store.ListModerationLogs(ctx, a.tenantID, nil, 1, 0)
	if err != nil {
		t.Fatalf("ListModerationLogs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("logs len=%d want 1: %+v", len(got), got)
	}
	if got[0].TenantID != a.tenantID || got[0].APIKeyID != aSecond.apiKeyID || got[0].ReasonCode != "a-new" {
		t.Fatalf("page newest tenant A row mismatch: %+v", got[0])
	}

	apiKeyID := a.apiKeyID
	filtered, err := store.ListModerationLogs(ctx, a.tenantID, &apiKeyID, 10, 0)
	if err != nil {
		t.Fatalf("ListModerationLogs filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].APIKeyID != a.apiKeyID || filtered[0].ReasonCode != "a-old" {
		t.Fatalf("api_key_id filter mismatch: %+v", filtered)
	}
}

func TestListBannedKeys(t *testing.T) {
	// 变异:不做 moderation 违规凭证 join,直接列出所有 disabled/active 的
	// api_keys；active 和手动 disabled 的 key 就会泄漏进列表。
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	banned := seedModerationAPIKey(t, ctx, pool, "banned-auto", "disabled")
	active := seedModerationAPIKeyInTenant(t, ctx, pool, banned.tenantID, "banned-active", "active")
	manualDisabled := seedModerationAPIKeyInTenant(t, ctx, pool, banned.tenantID, "banned-manual", "disabled")
	banAt := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	insertModerationViolationAt(t, ctx, pool, banned, DecisionBlockKeyword, "threshold-hit", "hash-banned", banAt)
	setAPIKeyUpdatedAt(t, ctx, pool, banned, banAt.Add(time.Second))

	got, err := store.ListBannedAPIKeys(ctx, banned.tenantID, 10, 0)
	if err != nil {
		t.Fatalf("ListBannedAPIKeys: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("banned len=%d want 1; active=%d manual=%d rows=%+v",
			len(got), active.apiKeyID, manualDisabled.apiKeyID, got)
	}
	if got[0].ID != banned.apiKeyID || got[0].Status != "disabled" || got[0].ViolationCount != 1 {
		t.Fatalf("banned row mismatch: %+v", got[0])
	}
}

func TestUnbanReEnablesModerationDisabledKey(t *testing.T) {
	// 变异:把 EnableModerationAPIKey 里的 moderation_log 插入去掉；
	// status 会翻成 active,但下面的 admin_unban 审计断言会变红。
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	key := seedModerationAPIKey(t, ctx, pool, "unban-ok", "disabled")
	banAt := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	insertModerationViolationAt(t, ctx, pool, key, DecisionBlockKeyword, "threshold-hit", "hash-unban-ok", banAt)
	setAPIKeyUpdatedAt(t, ctx, pool, key, banAt.Add(time.Second))

	result, err := store.UnbanAPIKey(ctx, UnbanAPIKeyRequest{
		TenantID: key.tenantID,
		APIKeyID: key.apiKeyID,
		ActorID:  "1",
		Reason:   "manual review cleared",
	})
	if err != nil {
		t.Fatalf("UnbanAPIKey: %v", err)
	}
	if result.APIKeyID != key.apiKeyID || result.TenantID != key.tenantID ||
		result.Status != "active" || result.AuditLogID == 0 {
		t.Fatalf("unban result mismatch: %+v", result)
	}
	if status := apiKeyStatus(t, ctx, pool, key); status != "active" {
		t.Fatalf("api key status=%q want active", status)
	}
	if count := adminUnbanAuditCount(t, ctx, pool, key, "admin_unban_actor:1"); count != 1 {
		t.Fatalf("admin_unban audit rows=%d want 1", count)
	}
}

func TestUnbanRejectsNonModerationDisabledKey(t *testing.T) {
	// 变异:只按 id/status 启用,或者接受任意历史违规
	// 而不绑定 moderation-disable 时间戳；这个手动 disabled 的 key
	// 就会被错误地变成 active。
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	key := seedModerationAPIKey(t, ctx, pool, "unban-manual-disabled", "disabled")
	oldViolationAt := time.Date(2026, 6, 6, 8, 0, 0, 0, time.UTC)
	insertModerationViolationAt(t, ctx, pool, key, DecisionBlockKeyword, "old-violation", "hash-old-manual", oldViolationAt)
	setAPIKeyUpdatedAt(t, ctx, pool, key, oldViolationAt.Add(2*time.Hour))

	_, err := store.UnbanAPIKey(ctx, UnbanAPIKeyRequest{
		TenantID: key.tenantID,
		APIKeyID: key.apiKeyID,
		ActorID:  "1",
		Reason:   "not a moderation disable",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UnbanAPIKey err=%v want ErrNotFound", err)
	}
	if status := apiKeyStatus(t, ctx, pool, key); status != "disabled" {
		t.Fatalf("non-moderation disabled key status=%q want disabled", status)
	}
	if count := adminUnbanAuditCount(t, ctx, pool, key, "admin_unban_actor:1"); count != 0 {
		t.Fatalf("unexpected admin_unban audit rows=%d", count)
	}
}

func TestUnbanTenantIsolation(t *testing.T) {
	// 变异:把 tenant_id 从 EnableModerationAPIKey 里去掉；租户 A 就能凭 id
	// 启用租户 B 的 disabled key,本 status 断言随之变红。
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	tenantAKey := seedModerationAPIKey(t, ctx, pool, "unban-tenant-a", "active")
	tenantBKey := seedModerationAPIKey(t, ctx, pool, "unban-tenant-b", "disabled")
	banAt := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	insertModerationViolationAt(t, ctx, pool, tenantBKey, DecisionBlockKeyword, "threshold-hit", "hash-unban-tenant-b", banAt)
	setAPIKeyUpdatedAt(t, ctx, pool, tenantBKey, banAt.Add(time.Second))

	_, err := store.UnbanAPIKey(ctx, UnbanAPIKeyRequest{
		TenantID: tenantAKey.tenantID,
		APIKeyID: tenantBKey.apiKeyID,
		ActorID:  "1",
		Reason:   "wrong tenant",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant UnbanAPIKey err=%v want ErrNotFound", err)
	}
	if status := apiKeyStatus(t, ctx, pool, tenantBKey); status != "disabled" {
		t.Fatalf("tenant B key status=%q want disabled", status)
	}
}

type moderationAPIKeySeed struct {
	tenantID int64
	userID   int64
	apiKeyID int64
}

func openModerationIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open integration pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedModerationAPIKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string, status string) moderationAPIKeySeed {
	t.Helper()
	tenantName := fmt.Sprintf("moderation-%s-%d", suffix, time.Now().UnixNano())
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		tenantName,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM moderation_log WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM moderation_violation_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return seedModerationAPIKeyInTenant(t, ctx, pool, tenantID, suffix, status)
}

func seedModerationAPIKeyInTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, suffix string, status string) moderationAPIKeySeed {
	t.Helper()
	var userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user-"+suffix,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var apiKeyID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		tenantID, userID, "key-"+suffix,
		"$2a$10$placeholder-not-resolved-by-moderation-admin",
		"hk_test_"+suffix, status,
	).Scan(&apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	return moderationAPIKeySeed{tenantID: tenantID, userID: userID, apiKeyID: apiKeyID}
}

func insertModerationLogAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key moderationAPIKeySeed, decision Decision, reason string, payloadHash string, occurredAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO moderation_log (
			tenant_id, api_key_id, user_id, payload_hash, decision, reason_code, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		key.tenantID, key.apiKeyID, key.userID, payloadHash, string(decision), reason, occurredAt,
	); err != nil {
		t.Fatalf("insert moderation_log: %v", err)
	}
}

func insertModerationViolationAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key moderationAPIKeySeed, decision Decision, reason string, payloadHash string, occurredAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO moderation_violation_events (
			tenant_id, api_key_id, user_id, payload_hash, decision, reason_code, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		key.tenantID, key.apiKeyID, key.userID, payloadHash, string(decision), reason, occurredAt,
	); err != nil {
		t.Fatalf("insert moderation_violation_events: %v", err)
	}
}

func apiKeyStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key moderationAPIKeySeed) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM api_keys WHERE tenant_id=$1 AND id=$2`,
		key.tenantID, key.apiKeyID,
	).Scan(&status); err != nil {
		t.Fatalf("query api key status: %v", err)
	}
	return status
}

func setAPIKeyUpdatedAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key moderationAPIKeySeed, updatedAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE api_keys SET updated_at=$3 WHERE tenant_id=$1 AND id=$2`,
		key.tenantID, key.apiKeyID, updatedAt,
	); err != nil {
		t.Fatalf("set api key updated_at: %v", err)
	}
}

func adminUnbanAuditCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key moderationAPIKeySeed, requestID string) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::bigint
		   FROM moderation_log
		  WHERE tenant_id=$1
		    AND api_key_id=$2
		    AND request_id=$3
		    AND reason_code LIKE 'admin_unban%'`,
		key.tenantID, key.apiKeyID, requestID,
	).Scan(&count); err != nil {
		t.Fatalf("query admin_unban audit count: %v", err)
	}
	return count
}
