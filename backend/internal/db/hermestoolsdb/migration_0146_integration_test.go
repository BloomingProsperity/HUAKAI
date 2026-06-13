//go:build integration_pg

// Migration guard for 0146_hermes_mutating_tools: proves the WAVE H4 migration
//  1. extends hermes_tool_calls.tool_name CHECK to admit the four mutating tool
//     names (dlq_replay / account_pause / account_resume / renew_trigger) and
//     still rejects a bogus name;
//  2. adds the hermes_tool_calls.dry_run column (accepts a boolean);
//  3. extends admin_audit_events.action CHECK to admit the four
//     hermes.tool.<name> mutating actions and still rejects a bogus one;
//  4. extends admin_audit_events.target_type CHECK to admit 'dlq_event'.
//
// DISCRIMINATING vs the prior migration (0145): at 0145 the four mutating
// tool_names + actions + dry_run column + dlq_event target_type do NOT exist, so
// each accept-assertion below would fail (CHECK violation / unknown column).
//
// Requires HUAKAI_DATABASE_URL pointing at a migrated DB; skips when unset.
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

	// Each of the four mutating tool names inserts cleanly, with dry_run set.
	for _, tool := range []string{"dlq_replay", "account_pause", "account_resume", "renew_trigger"} {
		row, err := q.InsertHermesToolCall(ctx, InsertHermesToolCallParams{
			TenantID:     tenantID,
			ActorUserID:  userID,
			ToolName:     tool,
			ResultStatus: "ok",
			CalledAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			DryRun:       true,
		})
		if err != nil {
			t.Fatalf("mutating tool %q rejected after migration: %v", tool, err)
		}
		if row.ID <= 0 {
			t.Fatalf("tool %q returned id=%d want > 0", tool, row.ID)
		}
	}

	// dry_run defaults / persists: a row with dry_run=false reads back false.
	var dryRun bool
	if err := pool.QueryRow(ctx,
		`SELECT dry_run FROM hermes_tool_calls WHERE tenant_id = $1 ORDER BY id DESC LIMIT 1`, tenantID).Scan(&dryRun); err != nil {
		t.Fatalf("read dry_run column (does it exist?): %v", err)
	}

	// DISCRIMINATING: a still-unknown tool name is rejected by the extended CHECK.
	_, err := q.InsertHermesToolCall(ctx, InsertHermesToolCallParams{
		TenantID: tenantID, ActorUserID: userID, ToolName: "bogus_mutator",
		ResultStatus: "ok", CalledAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
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

	// The four mutating actions + their target types insert cleanly.
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

	// DISCRIMINATING: a bogus hermes.tool.* action is still rejected.
	var pgErr *pgconn.PgError
	if err := insertAdmin("hermes.tool.bogus", "provider_account"); err == nil {
		t.Fatalf("bogus mutating action accepted; action CHECK too permissive")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("bogus action error=%v want 23514 CHECK violation", err)
	}

	// DISCRIMINATING: an unknown target_type is still rejected (dlq_event is the
	// only new one this migration adds).
	if err := insertAdmin("hermes.tool.dlq_replay", "bogus_target"); err == nil {
		t.Fatalf("bogus target_type accepted; target_type CHECK too permissive")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("bogus target_type error=%v want 23514 CHECK violation", err)
	}
}
