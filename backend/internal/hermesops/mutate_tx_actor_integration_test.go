//go:build integration_pg

package hermesops

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	rootdb "github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openActorPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("HUAKAI_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL 或 HUAKAI_DATABASE_URL")
	}
	pool, err := rootdb.Open(ctx, rootdb.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("连接 PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type actorFixture struct {
	tenantID int64
	tokenID  int64
	userID   int64
}

func seedActorFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) actorFixture {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var fixture actorFixture
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "hermes-actor-"+suffix,
	).Scan(&fixture.tenantID); err != nil {
		t.Fatalf("创建测试租户: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id)
		 VALUES ($1, $2, $3, 'tenant_operator', $4) RETURNING id`,
		"actor-token-"+suffix, "hash-"+suffix, "prefix-"+suffix[:8], fixture.tenantID,
	).Scan(&fixture.tokenID); err != nil {
		t.Fatalf("创建管理员令牌: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, display_name, status, role, principal_kind)
		 VALUES ($1, $2, 'Hermes 会话管理员', 'active', 'admin', 'human') RETURNING id`,
		fixture.tenantID, "hermes-actor-"+suffix+"@example.invalid",
	).Scan(&fixture.userID); err != nil {
		t.Fatalf("创建会话管理员: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_tool_calls WHERE tenant_id=$1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_tokens WHERE id=$1`, fixture.tokenID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, fixture.userID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, fixture.tenantID)
	})
	return fixture
}

func TestOrchestratorActorIdentityRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openActorPool(t, ctx)
	fixture := seedActorFixture(t, ctx, pool)
	orchestrator := NewMutateOrchestrator(pool)

	cases := []struct {
		name        string
		source      string
		actorID     int64
		wantAuditID string
	}{
		{
			name:        "管理员令牌",
			source:      admin.AdminSourceToken,
			actorID:     fixture.tokenID,
			wantAuditID: "admin_token:" + strconv.FormatInt(fixture.tokenID, 10),
		},
		{
			name:        "管理员会话",
			source:      admin.AdminSourceSession,
			actorID:     fixture.userID,
			wantAuditID: "admin_user:" + strconv.FormatInt(fixture.userID, 10),
		},
	}

	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			correlationID := fmt.Sprintf("actor-%d-%d", index, time.Now().UnixNano())
			requestID := "request-" + correlationID
			record := MutationAuditRecord{
				TenantID:      fixture.tenantID,
				ActorSource:   testCase.source,
				ActorID:       testCase.actorID,
				ActorRole:     RoleTenantOperator,
				ToolName:      ToolAccountPause,
				Status:        ResultOK,
				CorrelationID: correlationID,
				RequestID:     requestID,
				AdminAction:   "hermes.tool.account_pause",
				TargetType:    "provider_account",
				TargetID:      5,
				AuditPayload:  map[string]any{"account_id": int64(5)},
			}
			_, err := orchestrator.Execute(ctx, "hermes:actor:"+correlationID, record,
				func(context.Context, pgx.Tx) (ToolResult, error) {
					return ToolResult{Summary: map[string]any{"enabled": false}}, nil
				})
			if err != nil {
				t.Fatalf("执行工具: %v", err)
			}

			var gotSource, gotRole, gotCategory string
			var gotActorID int64
			if err := pool.QueryRow(ctx, `SELECT actor_source, actor_id, actor_role, log_category
				FROM hermes_tool_calls
				WHERE tenant_id=$1 AND correlation_id=$2`,
				fixture.tenantID, correlationID,
			).Scan(&gotSource, &gotActorID, &gotRole, &gotCategory); err != nil {
				t.Fatalf("读取工具日志: %v", err)
			}
			if gotSource != testCase.source || gotActorID != testCase.actorID ||
				gotRole != RoleTenantOperator || gotCategory != "operation" {
				t.Fatalf("工具日志归属=(%s,%d,%s,%s)，期望=(%s,%d,%s,operation)",
					gotSource, gotActorID, gotRole, gotCategory,
					testCase.source, testCase.actorID, RoleTenantOperator)
			}

			var gotAuditID, gotAuditRole string
			if err := pool.QueryRow(ctx, `SELECT actor_id, actor_role
				FROM admin_audit_events
				WHERE tenant_id=$1 AND request_id=$2 AND action='hermes.tool.account_pause'`,
				fixture.tenantID, requestID,
			).Scan(&gotAuditID, &gotAuditRole); err != nil {
				t.Fatalf("读取管理员日志: %v", err)
			}
			if gotAuditID != testCase.wantAuditID || gotAuditRole != RoleTenantOperator {
				t.Fatalf("管理员日志归属=(%s,%s)，期望=(%s,%s)",
					gotAuditID, gotAuditRole, testCase.wantAuditID, RoleTenantOperator)
			}
		})
	}
}

func TestOrchestratorRejectsMissingActorRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openActorPool(t, ctx)
	fixture := seedActorFixture(t, ctx, pool)
	correlationID := "missing-actor-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	record := MutationAuditRecord{
		TenantID:      fixture.tenantID,
		ActorSource:   admin.AdminSourceToken,
		ActorID:       0,
		ActorRole:     RoleTenantOperator,
		ToolName:      ToolAccountPause,
		Status:        ResultOK,
		CorrelationID: correlationID,
		AdminAction:   "hermes.tool.account_pause",
		TargetType:    "provider_account",
		TargetID:      5,
	}
	_, err := NewMutateOrchestrator(pool).Execute(ctx, "hermes:actor:"+correlationID, record,
		func(context.Context, pgx.Tx) (ToolResult, error) {
			return ToolResult{Summary: map[string]any{"enabled": false}}, nil
		})
	if err == nil {
		t.Fatal("缺失操作者 ID 时执行成功")
	}
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM hermes_tool_calls WHERE tenant_id=$1 AND correlation_id=$2`,
		fixture.tenantID, correlationID,
	).Scan(&count); err != nil {
		t.Fatalf("统计回滚日志: %v", err)
	}
	if count != 0 {
		t.Fatalf("失败事务留下 %d 条工具日志", count)
	}
}
