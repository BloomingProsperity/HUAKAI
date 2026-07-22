//go:build integration_pg

package adminobservabilityhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

func openAdminObservabilityIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	}
	if dsn == "" {
		t.Fatal("integration_pg 必须设置 HUAKAI_TEST_DATABASE_URL 或 HUAKAI_DATABASE_URL")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 8})
	if err != nil {
		t.Fatalf("打开 PostgreSQL：%v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestAdminObsUsageProjectsClientIPAndUA —— 审计闭环证明（真实 PG）。
//
// 它守护的缺陷：ip_address/user_agent 已持久化在 usage_records 上
// （迁移 0112），但 admin 可观测性 usage 列表从未把它们投影出来。
// 本测试播种一条携带已知 ip/ua 的 usage_record，驱动真实的 admin usage
// handler（真实 ListUsageRecords 查询 → 真实 mapUsageRow 投影，基于真实的
// *pgxpool.Pool store），并断言 JSON 响应携带完全一致的值。
//
// 判别性夹具：播种的 ip/ua（"203.0.113.7" / "probe-UA/1.0"）是有辨识度的
// 哨兵值 —— 若投影丢掉这些列、输出 "" / nil 或扫描了错误的列，则响应体
// 不会同时包含这两个哨兵，断言便会变红。（已通过清空 mapUsageRow 输出做变异验证。）
func TestAdminObsUsageProjectsClientIPAndUA(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openAdminObservabilityIntegrationPool(t, ctx)
	suffix := fmt.Sprintf("obs-ipua-%d", time.Now().UnixNano())
	const wantIP = "203.0.113.7"
	const wantUA = "probe-UA/1.0"
	const wantClientTool = "claude_code"

	fx := seedObsIPUARecord(t, ctx, pool, suffix, wantIP, wantUA, wantClientTool)

	store := dbbilling.New(pool)
	deps := obsRealDepsStub{tenantID: fx.tenantID, store: store}
	h := NewUsageHandler(deps)
	rec := invokeObs(h, fmt.Sprintf("/admin/v1/usage?tenant_id=%d&limit=50", fx.tenantID))
	assertStatus(t, rec, http.StatusOK)

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode admin usage body: %v body=%s", err, rec.Body.String())
	}
	item, ok := findUsageItemByRequestID(body.Items, fx.logicalRequestID)
	if !ok {
		t.Fatalf("seeded usage row not returned by admin list: body=%s", rec.Body.String())
	}
	if got := jsonString(item["ip_address"]); got != wantIP {
		t.Fatalf("admin usage ip_address=%q want %q (close-loop projection broken) item=%v", got, wantIP, item)
	}
	if got := jsonString(item["user_agent"]); got != wantUA {
		t.Fatalf("admin usage user_agent=%q want %q (close-loop projection broken) item=%v", got, wantUA, item)
	}
	// client_tool（迁移 0137）走同一条闭环：已持久化但在接入
	// ListUsageRecords + mapUsageRow 之前未被投影。哨兵值为
	// "claude_code" —— 清空 mapUsageRow 的 client_tool 键会让此处变红。
	if got := jsonString(item["client_tool"]); got != wantClientTool {
		t.Fatalf("admin usage client_tool=%q want %q (close-loop projection broken) item=%v", got, wantClientTool, item)
	}
}

// obsRealDepsStub 为 admin 可观测性 handler 提供基于真实 pool 的真实 Queries
// store，以及一个固定的 tenant-operator 身份（范围限定在播种的租户），
// 使 parseTenantScope 接受该请求。
type obsRealDepsStub struct {
	tenantID int64
	store    AdminObservabilityStore
}

func (d obsRealDepsStub) AdminObservabilityAuth() AdminObservabilityAuth {
	return obsRealAuthStub{tenantID: d.tenantID}
}
func (d obsRealDepsStub) AdminObservabilityStore() AdminObservabilityStore { return d.store }

type obsRealAuthStub struct{ tenantID int64 }

func (a obsRealAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return admin.AdminIdentity{TokenID: 1, Role: admin.RoleTenantOperator, ScopeTenantID: a.tenantID}, nil
}

type obsIPUAFixture struct {
	tenantID         int64
	logicalRequestID string
}

func seedObsIPUARecord(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix, ip, ua, clientTool string) obsIPUAFixture {
	t.Helper()
	var fx obsIPUAFixture
	fx.logicalRequestID = "lr-" + suffix

	var tenantID, userID, apiKeyID, providerID, poolID, channelID, providerAccountID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, suffix).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	fx.tenantID = tenantID
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, email, display_name) VALUES ($1, $2, $3) RETURNING id`, tenantID, suffix+"@example.test", "Obs IPUA").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix) VALUES ($1, $2, $3, $4, $5) RETURNING id`, tenantID, userID, suffix, "hash-"+suffix, "prefix-"+suffix[:8]).Scan(&apiKeyID); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, $4) RETURNING id`, tenantID, "provider-"+suffix, "Provider "+suffix, "anthropic_messages").Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, "pool-"+suffix).Scan(&poolID); err != nil {
		t.Fatalf("insert pool: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, tenantID, poolID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, health_state) VALUES ($1, $2, $3, $4, 'api_key', 'healthy') RETURNING id`, tenantID, providerID, channelID, "account-"+suffix).Scan(&providerAccountID); err != nil {
		t.Fatalf("insert provider account: %v", err)
	}

	settledAt := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	acquisitionToken := uuid.New()
	var claimID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			provider_account_id, acquisition_token, attempt_seq, billing_policy_version,
			request_class, predicted_cost, actual_cost, currency_code, status, settled_at,
			lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'messages', 'claude-obs-ipua', $7,
			$8, $9, 1, 'bp-test', 'standard', 0.01000000, 0.01000000, 'USD',
			'committed', $10, $11)
		RETURNING id
	`, tenantID, "idem-"+suffix, "fingerprint-"+suffix, apiKeyID, userID, fx.logicalRequestID, poolID, providerAccountID, acquisitionToken, settledAt, settledAt.Add(time.Hour)).Scan(&claimID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, tokens_input, tokens_output, actual_cost,
			input_cost, output_cost, end_class, usage_source, pending_reconciliation,
			requested_at, settled_at, requested_model, upstream_model, stream,
			settlement_source, ip_address, user_agent, client_tool
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1, 10, 20, 0.01000000, 0.00400000,
			0.00600000, 'non_streaming', 'reported', false, $7, $8, 'claude-obs-ipua',
			'claude-obs-ipua-upstream', false, 'provider_upstream', $9, $10, $11)
	`, tenantID, claimID, apiKeyID, userID, providerAccountID, acquisitionToken, settledAt.Add(-time.Second), settledAt, ip, ua, clientTool); err != nil {
		t.Fatalf("insert usage record: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM usage_records WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_ledger_claims WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return fx
}

func findUsageItemByRequestID(items []map[string]any, requestID string) (map[string]any, bool) {
	for _, it := range items {
		if jsonString(it["request_id"]) == requestID {
			return it, true
		}
	}
	return nil, false
}

func jsonString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
