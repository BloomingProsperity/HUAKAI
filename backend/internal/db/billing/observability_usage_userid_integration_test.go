//go:build integration_pg

package billing

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestListUsageRecordsUserIDFilter 验证 ListUsageRecords 新增的 user_id 过滤($15)在真库上正确收敛:
// 同一租户下两个用户各有用量记录,按某用户过滤只返回该用户自己的,绝不泄露另一用户的;
// 不传 user_id(nil)则返回全部(向后兼容,既有 API-key 端点不受影响)。
//
// 这是会话级用量端点(/v1/me/usage-records,按 session user 收敛)防越权的根证据。
// MUTATION:若 $15 过滤被去掉,按 userA 过滤会返回 3 行(含 userB 的)而非 2 行 → 本测试变红。
func TestListUsageRecordsUserIDFilter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageUserScopePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fx := seedUsageUserScopeFixture(t, ctx, tx)
	q := New(tx)

	// 按 userA 过滤 → 只见 A 的 2 条。
	rowsA, err := q.ListUsageRecords(ctx, ListUsageRecordsParams{TenantID: &fx.tenantID, UserID: &fx.userA, PageLimit: 50})
	if err != nil {
		t.Fatalf("list userA: %v", err)
	}
	assertAllUserID(t, rowsA, fx.userA, 2)

	// 按 userB 过滤 → 只见 B 的 1 条,绝不含 A 的。
	rowsB, err := q.ListUsageRecords(ctx, ListUsageRecordsParams{TenantID: &fx.tenantID, UserID: &fx.userB, PageLimit: 50})
	if err != nil {
		t.Fatalf("list userB: %v", err)
	}
	assertAllUserID(t, rowsB, fx.userB, 1)

	// 不传 user_id(nil)→ 租户内全部 3 条(向后兼容:既有 API-key 端点行为不变)。
	rowsAll, err := q.ListUsageRecords(ctx, ListUsageRecordsParams{TenantID: &fx.tenantID, PageLimit: 50})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(rowsAll) != 3 {
		t.Fatalf("nil user_id 应返回全部 3 条,实得 %d", len(rowsAll))
	}
}

type usageUserScopeFixture struct {
	tenantID          int64
	userA, userB      int64
	apiKeyA, apiKeyB  int64
	providerAccountID int64
	poolID            int64
}

func openUsageUserScopePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping user-id scope integration test")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

func seedUsageUserScopeFixture(t *testing.T, ctx context.Context, tx pgx.Tx) usageUserScopeFixture {
	t.Helper()
	suffix := fmt.Sprintf("usage-userscope-%d", time.Now().UnixNano())
	var f usageUserScopeFixture
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, suffix).Scan(&f.tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO users (tenant_id, email, display_name) VALUES ($1, $2, $3) RETURNING id`, f.tenantID, suffix+"-a@example.test", "User A").Scan(&f.userA); err != nil {
		t.Fatalf("insert userA: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO users (tenant_id, email, display_name) VALUES ($1, $2, $3) RETURNING id`, f.tenantID, suffix+"-b@example.test", "User B").Scan(&f.userB); err != nil {
		t.Fatalf("insert userB: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix) VALUES ($1, $2, $3, $4, $5) RETURNING id`, f.tenantID, f.userA, suffix+"-a", "hash-a-"+suffix, "pa-"+suffix[:8]).Scan(&f.apiKeyA); err != nil {
		t.Fatalf("insert apiKeyA: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix) VALUES ($1, $2, $3, $4, $5) RETURNING id`, f.tenantID, f.userB, suffix+"-b", "hash-b-"+suffix, "pb-"+suffix[:8]).Scan(&f.apiKeyB); err != nil {
		t.Fatalf("insert apiKeyB: %v", err)
	}
	var providerID int64
	if err := tx.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, $4) RETURNING id`, f.tenantID, "provider-"+suffix, "Provider "+suffix, "anthropic_messages").Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, f.tenantID, "pool-"+suffix).Scan(&f.poolID); err != nil {
		t.Fatalf("insert pool: %v", err)
	}
	var channelID int64
	if err := tx.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, f.tenantID, f.poolID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, health_state) VALUES ($1, $2, $3, $4, 'api_key', 'healthy') RETURNING id`, f.tenantID, providerID, channelID, "account-"+suffix).Scan(&f.providerAccountID); err != nil {
		t.Fatalf("insert provider account: %v", err)
	}

	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	// userA:2 条;userB:1 条。
	seedUsageUserScopeRecord(t, ctx, tx, f, f.userA, f.apiKeyA, suffix+"-a1", base.Add(time.Second))
	seedUsageUserScopeRecord(t, ctx, tx, f, f.userA, f.apiKeyA, suffix+"-a2", base.Add(2*time.Second))
	seedUsageUserScopeRecord(t, ctx, tx, f, f.userB, f.apiKeyB, suffix+"-b1", base.Add(3*time.Second))
	return f
}

func seedUsageUserScopeRecord(t *testing.T, ctx context.Context, tx pgx.Tx, f usageUserScopeFixture, userID, apiKeyID int64, logicalRequestID string, settledAt time.Time) {
	t.Helper()
	acquisitionToken := uuid.New()
	var claimID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			provider_account_id, acquisition_token, attempt_seq, billing_policy_version,
			request_class, predicted_cost, actual_cost, currency_code, status, settled_at,
			lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'messages', 'claude-userscope', $7,
			$8, $9, 1, 'bp-test', 'standard', 0.01000000, 0.01000000, 'USD',
			'committed', $10, $11)
		RETURNING id
	`, f.tenantID, "idem-"+logicalRequestID, "fingerprint-"+logicalRequestID, apiKeyID, userID, logicalRequestID, f.poolID, f.providerAccountID, acquisitionToken, settledAt, settledAt.Add(time.Hour)).Scan(&claimID); err != nil {
		t.Fatalf("insert claim %s: %v", logicalRequestID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, tokens_input, tokens_output, actual_cost,
			input_cost, output_cost, end_class, usage_source, pending_reconciliation,
			requested_at, settled_at, requested_model, upstream_model, stream,
			settlement_source
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1, 10, 20, 0.01000000, 0.00400000,
			0.00600000, 'non_streaming', 'reported', false, $7, $8, 'claude-userscope',
			'claude-userscope-upstream', false, 'provider_upstream')
	`, f.tenantID, claimID, apiKeyID, userID, f.providerAccountID, acquisitionToken, settledAt.Add(-time.Second), settledAt); err != nil {
		t.Fatalf("insert usage %s: %v", logicalRequestID, err)
	}
}

func assertAllUserID(t *testing.T, rows []ListUsageRecordsRow, wantUserID int64, wantCount int) {
	t.Helper()
	if len(rows) != wantCount {
		t.Fatalf("行数=%d 期望=%d", len(rows), wantCount)
	}
	for i, row := range rows {
		if row.UserID != wantUserID {
			t.Fatalf("行 %d user_id=%d 期望=%d(用户维度过滤泄露了他人记录!)", i, row.UserID, wantUserID)
		}
	}
}
