//go:build integration_pg

// 0146_hermes_mutating_tools 的迁移守卫:证明 WAVE H4 这次迁移
//  1. 扩展 hermes_tool_calls.tool_name 的 CHECK，允许四个 mutating 工具名
//     (dlq_replay / account_pause / account_resume / renew_trigger)，同时仍
//     拒绝伪造的名字;
//  2. 新增 hermes_tool_calls.dry_run 列(接受布尔值);
//  3. 扩展 admin_audit_events.action 的 CHECK，允许四个 hermes.tool.<name>
//     mutating action，同时仍拒绝伪造的;
//  4. 扩展 admin_audit_events.target_type 的 CHECK，允许 'dlq_event'。
//
// 相对上一次迁移(0145)的判别力:在 0145 时，这四个 mutating tool_name +
// action + dry_run 列 + dlq_event target_type 都不存在，因此下面每条 accept
// 断言都会失败(CHECK 违反 / 未知列)。
//
// 需要 HUAKAI_DATABASE_URL 指向一个已迁移的 DB;未设置时跳过。
package hermestoolsdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestMigration0146_MutatingToolNamesAndDryRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openToolCallsPool(t, ctx)
	tenantID, userID := seedTenantUser(t, ctx, pool)
	q := New(pool)

	// 四个 mutating 工具名各自都能干净插入，且设置了 dry_run。
	for _, tool := range []string{"dlq_replay", "account_pause", "account_resume", "renew_trigger"} {
		row, err := q.InsertHermesToolCall(ctx, InsertHermesToolCallParams{
			TenantID:     tenantID,
			ActorSource:  "session",
			ActorID:      userID,
			ActorRole:    "tenant_operator",
			ToolName:     tool,
			ResultStatus: "ok",
			CalledAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			DryRun:       true,
			LogCategory:  "operation",
		})
		if err != nil {
			t.Fatalf("mutating tool %q rejected after migration: %v", tool, err)
		}
		if row.ID <= 0 {
			t.Fatalf("tool %q returned id=%d want > 0", tool, row.ID)
		}
	}

	// dry_run 的默认值 / 持久化:dry_run=false 的行读回来仍是 false。
	var dryRun bool
	if err := pool.QueryRow(ctx,
		`SELECT dry_run FROM hermes_tool_calls WHERE tenant_id = $1 ORDER BY id DESC LIMIT 1`, tenantID).Scan(&dryRun); err != nil {
		t.Fatalf("read dry_run column (does it exist?): %v", err)
	}

	// 判别力:一个仍然未知的工具名会被扩展后的 CHECK 拒绝。
	_, err := q.InsertHermesToolCall(ctx, InsertHermesToolCallParams{
		TenantID: tenantID, ActorSource: "session", ActorID: userID,
		ActorRole: "tenant_operator", ToolName: "bogus_mutator",
		ResultStatus: "ok", CalledAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		LogCategory: "operation",
	})
	if err == nil {
		t.Fatalf("bogus tool_name accepted; tool_name CHECK is too permissive")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "hermes_tool_calls_tool_name_check" {
		t.Fatalf("bogus tool error=%v want 23514 hermes_tool_calls_tool_name_check", err)
	}
}

func TestMigration0146_AdminAuditMutatingActionsAndTargetType(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openToolCallsPool(t, ctx)
	tenantID, _ := seedTenantUser(t, ctx, pool)

	insertAdmin := func(action, targetType string) error {
		_, e := pool.Exec(ctx,
			`INSERT INTO admin_audit_events (tenant_id, actor_id, actor_role, action, target_type, target_id)
			 VALUES ($1, '99', 'platform_admin', $2, $3, 1)`, tenantID, action, targetType)
		return e
	}

	// 四个 mutating action 及其 target type 都能干净插入。
	cases := []struct{ action, target string }{
		{"hermes.tool.dlq_replay", "dlq_event"},
		{"hermes.tool.account_pause", "provider_account"},
		{"hermes.tool.account_resume", "provider_account"},
		{"hermes.tool.renew_trigger", "account_credential"},
	}
	for _, c := range cases {
		if err := insertAdmin(c.action, c.target); err != nil {
			t.Fatalf("admin audit (%s,%s) rejected after migration: %v", c.action, c.target, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_audit_events WHERE tenant_id = $1`, tenantID)
	})

	// 判别力:伪造的 hermes.tool.* action 仍会被拒绝。
	var pgErr *pgconn.PgError
	if err := insertAdmin("hermes.tool.bogus", "provider_account"); err == nil {
		t.Fatalf("bogus mutating action accepted; action CHECK too permissive")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("bogus action error=%v want 23514 CHECK violation", err)
	}

	// 判别力:未知的 target_type 仍会被拒绝(本次迁移只新增了 dlq_event
	// 这一个)。
	if err := insertAdmin("hermes.tool.dlq_replay", "bogus_target"); err == nil {
		t.Fatalf("bogus target_type accepted; target_type CHECK too permissive")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("bogus target_type error=%v want 23514 CHECK violation", err)
	}
}
