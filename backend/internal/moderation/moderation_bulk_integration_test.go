//go:build integration_pg

package moderation

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
)

func TestBulkCreateKeywords_AllInserted(t *testing.T) {
	// 变异:只插入第一条有效项；accepted 会变成 1,下面的
	// stored keyword 数量/断言随之变红。
	ctx := context.Background()
	pool := openModerationBulkIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	tenantID := seedModerationBulkTenant(t, ctx, pool, "bulk-keywords-all")

	result, err := store.BulkCreateKeywords(ctx, BulkCreateKeywordsRequest{
		TenantID:  tenantID,
		UpdatedBy: "admin-1",
		Items: []BulkCreateKeywordItem{
			{Keyword: "alpha", ReasonCode: "", Enabled: true},
			{Keyword: "beta", ReasonCode: "policy_beta", Enabled: false},
			{Keyword: "gamma", ReasonCode: "policy_gamma", Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("BulkCreateKeywords: %v", err)
	}
	if result.Accepted != 3 || result.SkippedDuplicate != 0 || len(result.Errors) != 0 {
		t.Fatalf("bulk result=%+v want accepted=3 skipped=0 errors=0", result)
	}
	rows := moderationKeywordRows(t, ctx, pool, tenantID)
	if len(rows) != 3 {
		t.Fatalf("stored keyword rows=%d want 3: %+v", len(rows), rows)
	}
	if rows["alpha"].ReasonCode != "keyword_match" || !rows["alpha"].Enabled {
		t.Fatalf("alpha row mismatch: %+v", rows["alpha"])
	}
	if rows["beta"].ReasonCode != "policy_beta" || rows["beta"].Enabled {
		t.Fatalf("beta row mismatch: %+v", rows["beta"])
	}
	if rows["gamma"].ReasonCode != "policy_gamma" || !rows["gamma"].Enabled {
		t.Fatalf("gamma row mismatch: %+v", rows["gamma"])
	}
}

func TestBulkCreateKeywords_PartialSuccess(t *testing.T) {
	// 变异:校验时返回顶层 error,或把整批包成一个事务,
	// 让一条坏行回滚掉所有有效行；accepted/新增行断言随之变红。
	ctx := context.Background()
	pool := openModerationBulkIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	tenantID := seedModerationBulkTenant(t, ctx, pool, "bulk-keywords-partial")

	if _, err := store.CreateKeyword(ctx, CreateKeywordRequest{
		TenantID: tenantID, Keyword: "duplicate", ReasonCode: "existing", Enabled: true, UpdatedBy: "admin-1",
	}); err != nil {
		t.Fatalf("seed duplicate keyword: %v", err)
	}

	result, err := store.BulkCreateKeywords(ctx, BulkCreateKeywordsRequest{
		TenantID:  tenantID,
		UpdatedBy: "admin-1",
		Items: []BulkCreateKeywordItem{
			{Keyword: "new-one", ReasonCode: "policy_new_one", Enabled: true},
			{Keyword: " DUPLICATE ", ReasonCode: "policy_duplicate", Enabled: true},
			{Keyword: "   ", ReasonCode: "policy_invalid", Enabled: true},
			{Keyword: "new-two", ReasonCode: "", Enabled: false},
		},
	})
	if err != nil {
		t.Fatalf("BulkCreateKeywords top-level err=%v, want per-row collection", err)
	}
	if result.Accepted != 2 || result.SkippedDuplicate != 1 {
		t.Fatalf("bulk result=%+v want accepted=2 skipped_duplicate=1", result)
	}
	if len(result.Errors) != 1 || result.Errors[0].Index != 2 || result.Errors[0].Reason != "keyword_required" {
		t.Fatalf("row errors=%+v want index 2 keyword_required", result.Errors)
	}
	rows := moderationKeywordRows(t, ctx, pool, tenantID)
	if _, ok := rows["new-one"]; !ok {
		t.Fatalf("new-one missing after partial success: %+v", rows)
	}
	if row, ok := rows["new-two"]; !ok || row.Enabled {
		t.Fatalf("new-two missing or enabled mismatch after partial success: %+v", rows)
	}
	if duplicateCount := moderationKeywordCount(t, ctx, pool, tenantID, "duplicate"); duplicateCount != 1 {
		t.Fatalf("duplicate keyword rows=%d want 1", duplicateCount)
	}
}

func TestBulkCreateHashes_RejectsNonSHA256(t *testing.T) {
	// 变异:跳过 hash_hex 校验,把非法行直接发给 SQL；本用例
	// 要么观察到顶层 CHECK 失败,要么看到坏 hash 行泄漏进来。
	ctx := context.Background()
	pool := openModerationBulkIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	tenantID := seedModerationBulkTenant(t, ctx, pool, "bulk-hashes-sha")
	validA := strings.Repeat("a", 64)
	validB := strings.Repeat("b", 64)

	result, err := store.BulkCreateHashes(ctx, BulkCreateHashesRequest{
		TenantID:  tenantID,
		UpdatedBy: "admin-1",
		Items: []BulkCreateHashItem{
			{HashHex: validA, ReasonCode: "hash_a", Enabled: true},
			{HashHex: "not-a-sha-256", ReasonCode: "bad_hash", Enabled: true},
			{HashHex: validB, ReasonCode: "hash_b", Enabled: false},
		},
	})
	if err != nil {
		t.Fatalf("BulkCreateHashes top-level err=%v, want per-row collection", err)
	}
	if result.Accepted != 2 || result.SkippedDuplicate != 0 {
		t.Fatalf("bulk result=%+v want accepted=2 skipped_duplicate=0", result)
	}
	if len(result.Errors) != 1 || result.Errors[0].Index != 1 || result.Errors[0].Reason != "invalid_hash_hex" {
		t.Fatalf("row errors=%+v want index 1 invalid_hash_hex", result.Errors)
	}
	if count := moderationHashCount(t, ctx, pool, tenantID, "not-a-sha-256"); count != 0 {
		t.Fatalf("invalid hash rows=%d want 0", count)
	}
	if count := moderationHashCount(t, ctx, pool, tenantID, validA); count != 1 {
		t.Fatalf("validA hash rows=%d want 1", count)
	}
	if count := moderationHashCount(t, ctx, pool, tenantID, validB); count != 1 {
		t.Fatalf("validB hash rows=%d want 1", count)
	}
}

func TestBulk_TenantScoped(t *testing.T) {
	// 变异:把 tenant_id 从 bulk 插入/列出路径里去掉,或者全局去重；
	// 同一个 keyword 在两个租户里会被合并或泄漏,这些断言随之变红。
	ctx := context.Background()
	pool := openModerationBulkIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	tenantA := seedModerationBulkTenant(t, ctx, pool, "bulk-tenant-a")
	tenantB := seedModerationBulkTenant(t, ctx, pool, "bulk-tenant-b")

	for _, tenantID := range []int64{tenantA, tenantB} {
		result, err := store.BulkCreateKeywords(ctx, BulkCreateKeywordsRequest{
			TenantID:  tenantID,
			UpdatedBy: "admin-1",
			Items: []BulkCreateKeywordItem{
				{Keyword: "shared-keyword", ReasonCode: fmt.Sprintf("tenant_%d", tenantID), Enabled: true},
			},
		})
		if err != nil {
			t.Fatalf("BulkCreateKeywords tenant %d: %v", tenantID, err)
		}
		if result.Accepted != 1 || result.SkippedDuplicate != 0 || len(result.Errors) != 0 {
			t.Fatalf("tenant %d bulk result=%+v want one accepted row", tenantID, result)
		}
	}

	rowsA, err := store.ListKeywords(ctx, tenantA, 10, 0)
	if err != nil {
		t.Fatalf("ListKeywords tenant A: %v", err)
	}
	rowsB, err := store.ListKeywords(ctx, tenantB, 10, 0)
	if err != nil {
		t.Fatalf("ListKeywords tenant B: %v", err)
	}
	if len(rowsA) != 1 || rowsA[0].TenantID != tenantA || rowsA[0].Keyword != "shared-keyword" {
		t.Fatalf("tenant A rows=%+v want one tenant A shared-keyword", rowsA)
	}
	if len(rowsB) != 1 || rowsB[0].TenantID != tenantB || rowsB[0].Keyword != "shared-keyword" {
		t.Fatalf("tenant B rows=%+v want one tenant B shared-keyword", rowsB)
	}
}

type moderationKeywordSnapshot struct {
	ReasonCode string
	Enabled    bool
}

func openModerationBulkIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
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

func seedModerationBulkTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) int64 {
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
		_, _ = pool.Exec(c, `DELETE FROM moderation_hashes WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM moderation_keywords WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID
}

func moderationKeywordRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) map[string]moderationKeywordSnapshot {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT keyword, reason_code, enabled
		   FROM moderation_keywords
		  WHERE tenant_id=$1 AND deleted_at IS NULL`,
		tenantID,
	)
	if err != nil {
		t.Fatalf("query moderation_keywords: %v", err)
	}
	defer rows.Close()
	out := map[string]moderationKeywordSnapshot{}
	for rows.Next() {
		var keyword string
		var snapshot moderationKeywordSnapshot
		if err := rows.Scan(&keyword, &snapshot.ReasonCode, &snapshot.Enabled); err != nil {
			t.Fatalf("scan moderation_keywords: %v", err)
		}
		out[keyword] = snapshot
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate moderation_keywords: %v", err)
	}
	return out
}

func moderationKeywordCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, keyword string) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::bigint
		   FROM moderation_keywords
		  WHERE tenant_id=$1 AND lower(keyword)=lower($2) AND deleted_at IS NULL`,
		tenantID, keyword,
	).Scan(&count); err != nil {
		t.Fatalf("count moderation_keywords: %v", err)
	}
	return count
}

func moderationHashCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, hashHex string) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::bigint
		   FROM moderation_hashes
		  WHERE tenant_id=$1 AND hash_hex=$2 AND deleted_at IS NULL`,
		tenantID, hashHex,
	).Scan(&count); err != nil {
		t.Fatalf("count moderation_hashes: %v", err)
	}
	return count
}
