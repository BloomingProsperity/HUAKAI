//go:build integration_pg

// 0145_hermes_tool_calls 迁移守卫:证明 hermes_tool_calls 表
// 及其 tool_name / result_status CHECK 约束存在且具区分性
// (未知的 tool_name 会被拒绝),并证明迁移后 hermes_audit_events 的 action
// 白名单接受新增的 hermes.tool.<name> 动作。
//
// 需要 HUAKAI_DATABASE_URL 指向一个已迁移的 DB;未设置时干净跳过
// (与仓库内其它 integration_pg 守卫一致)。除插入并删除自身行外,不会迁移或
// 改动共享 schema。
package hermestoolsdb

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	rootdb "github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openToolCallsPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	p, err := rootdb.Open(ctx, rootdb.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// seedTenantUser 创建一个一次性的 tenant + user,使 (tenant_id,
// actor_id) 的值引用合理的 id;hermes_tool_calls 对它们没有 FK,
// 但真实的 tenant/user 让行更接近实际。返回 id 与 cleanup。
func seedTenantUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "tool-calls-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	var userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email) VALUES ($1, $2) RETURNING id`, tenantID, "tc-"+suffix+"@example.test").Scan(&userID); err != nil {
		// users schema 可能要求更多列;回退到一个合成 id ——
		// 该表没有 FK,所以对下面的 CHECK 区分性断言而言,
		// 不存在的 user id 也可接受。
		userID = 1
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM hermes_tool_calls WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return tenantID, userID
}

func TestHermesToolCallsTableAndCheckExist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openToolCallsPool(t, ctx)
	tenantID, userID := seedTenantUser(t, ctx, pool)
	q := New(pool)

	// 白名单内的 tool 名 + 合法 status 能干净插入。
	row, err := q.InsertHermesToolCall(ctx, InsertHermesToolCallParams{
		TenantID:      tenantID,
		ActorSource:   "session",
		ActorID:       userID,
		ActorRole:     "tenant_operator",
		ToolName:      "dlq_inspect",
		ResultStatus:  "ok",
		RequestedArgs: []byte(`{"status":"pending"}`),
		ResultSummary: []byte(`{"dlq_count":0}`),
		CalledAt:      pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		LogCategory:   "operation",
	})
	if err != nil {
		t.Fatalf("insert whitelisted tool: %v", err)
	}
	if row.ID <= 0 {
		t.Fatalf("returned id=%d want > 0", row.ID)
	}

	// 区分性:未知的 tool 名必须被 CHECK 拒绝。在上一个迁移版本下
	// 该表根本不存在,所以这条插入会报出不同的错误 —— 而在
	// 已迁移的 schema 上,它必须是一个点名 tool_name 约束的 23514 CHECK
	// 违例。
	_, err = q.InsertHermesToolCall(ctx, InsertHermesToolCallParams{
		TenantID:     tenantID,
		ActorSource:  "session",
		ActorID:      userID,
		ActorRole:    "tenant_operator",
		ToolName:     "totally_unknown_tool",
		ResultStatus: "ok",
		CalledAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		LogCategory:  "operation",
	})
	if err == nil {
		t.Fatalf("unknown tool_name was accepted; the CHECK constraint is missing or non-discriminating")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "hermes_tool_calls_tool_name_check" {
		t.Fatalf("unknown tool error=%v want 23514 hermes_tool_calls_tool_name_check", err)
	}

	// 错误的 result_status 也必须被拒绝(其 CHECK)。
	_, err = q.InsertHermesToolCall(ctx, InsertHermesToolCallParams{
		TenantID:     tenantID,
		ActorSource:  "session",
		ActorID:      userID,
		ActorRole:    "tenant_operator",
		ToolName:     "dlq_inspect",
		ResultStatus: "maybe",
		CalledAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		LogCategory:  "operation",
	})
	if err == nil {
		t.Fatalf("invalid result_status accepted; the CHECK constraint is missing")
	}
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "hermes_tool_calls_result_status_check" {
		t.Fatalf("bad result_status error=%v want 23514 hermes_tool_calls_result_status_check", err)
	}
}

func TestHermesAuditActionWhitelistAcceptsToolActions(t *testing.T) {
	// 回归:迁移必须扩展 hermes_audit_events_action_check 以
	// 接纳六个 hermes.tool.<name> 动作;否则 tool-execute 的
	// 审计镜像就会违反 CHECK。区分性:一个不属于这六个之一的
	// 伪造 hermes.tool.* 动作仍必须被拒绝。
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openToolCallsPool(t, ctx)
	tenantID, userID := seedTenantUser(t, ctx, pool)

	insertAudit := func(action string) error {
		_, e := pool.Exec(ctx,
			`INSERT INTO hermes_audit_events (
				ts, tenant_id, actor_source, actor_id, actor_role, action, result, log_category
			 ) VALUES (now(), $1, 'session', $2, 'tenant_operator', $3, 'success', 'operation')`,
			tenantID, userID, action)
		return e
	}

	for _, action := range []string{
		"hermes.tool.credential_diagnose",
		"hermes.tool.account_health_diagnose",
		"hermes.tool.request_diagnose",
		"hermes.tool.dlq_inspect",
		"hermes.tool.audit_lookup",
		"hermes.tool.log_analyze",
	} {
		if err := insertAudit(action); err != nil {
			t.Fatalf("audit action %q rejected after migration: %v", action, err)
		}
	}

	// 不在白名单内的 hermes.tool.* 动作仍必须被拒绝。
	err := insertAudit("hermes.tool.bogus_mutator")
	if err == nil {
		t.Fatalf("bogus hermes.tool.* action accepted; the whitelist is too permissive")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("bogus action error=%v want 23514 CHECK violation", err)
	}
}
