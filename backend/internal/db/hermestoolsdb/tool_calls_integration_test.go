//go:build integration_pg

// Migration guard for 0145_hermes_tool_calls: proves the hermes_tool_calls table
// + its tool_name / result_status CHECK constraints exist and are discriminating
// (an unknown tool_name is rejected), and that the hermes_audit_events action
// whitelist accepts the new hermes.tool.<name> actions after the migration.
//
// Requires HUAKAI_DATABASE_URL pointing at a migrated DB; skips cleanly when
// unset (mirrors the repo's other integration_pg guards). Does NOT migrate or
// mutate shared schema beyond inserting + deleting its own rows.
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

// seedTenantUser creates a throwaway tenant + user so the (tenant_id,
// actor_user_id) values reference plausible ids; hermes_tool_calls has no FK on
// them but a real tenant/user keeps the row realistic. Returns ids + cleanup.
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
		// users schema may require more columns; fall back to a synthetic id —
		// the table has no FK, so a non-existent user id is acceptable for the
		// CHECK-discrimination assertions below.
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

	// A whitelisted tool name + valid status inserts cleanly.
	row, err := q.InsertHermesToolCall(ctx, InsertHermesToolCallParams{
		TenantID:      tenantID,
		ActorUserID:   userID,
		ToolName:      "dlq_inspect",
		ResultStatus:  "ok",
		RequestedArgs: []byte(`{"status":"pending"}`),
		ResultSummary: []byte(`{"dlq_count":0}`),
		CalledAt:      pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if err != nil {
		t.Fatalf("insert whitelisted tool: %v", err)
	}
	if row.ID <= 0 {
		t.Fatalf("returned id=%d want > 0", row.ID)
	}

	// DISCRIMINATING: an UNKNOWN tool name must be rejected by the CHECK. At the
	// prior migration the table doesn't exist at all, so this insert would error
	// differently — here, on the migrated schema, it must be a 23514 CHECK
	// violation naming the tool_name constraint.
	_, err = q.InsertHermesToolCall(ctx, InsertHermesToolCallParams{
		TenantID:     tenantID,
		ActorUserID:  userID,
		ToolName:     "totally_unknown_tool",
		ResultStatus: "ok",
		CalledAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if err == nil {
		t.Fatalf("unknown tool_name was accepted; the CHECK constraint is missing or non-discriminating")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "hermes_tool_calls_tool_name_check" {
		t.Fatalf("unknown tool error=%v want 23514 hermes_tool_calls_tool_name_check", err)
	}

	// A bad result_status must also be rejected (its CHECK).
	_, err = q.InsertHermesToolCall(ctx, InsertHermesToolCallParams{
		TenantID:     tenantID,
		ActorUserID:  userID,
		ToolName:     "dlq_inspect",
		ResultStatus: "maybe",
		CalledAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if err == nil {
		t.Fatalf("invalid result_status accepted; the CHECK constraint is missing")
	}
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "hermes_tool_calls_result_status_check" {
		t.Fatalf("bad result_status error=%v want 23514 hermes_tool_calls_result_status_check", err)
	}
}

func TestHermesAuditActionWhitelistAcceptsToolActions(t *testing.T) {
	// Regression: the migration must extend hermes_audit_events_action_check to
	// admit the six hermes.tool.<name> actions; without it, the tool-execute
	// audit mirror would violate the CHECK. DISCRIMINATING: a bogus hermes.tool.*
	// action that is NOT one of the six must still be rejected.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openToolCallsPool(t, ctx)
	tenantID, userID := seedTenantUser(t, ctx, pool)

	insertAudit := func(action string) error {
		_, e := pool.Exec(ctx,
			`INSERT INTO hermes_audit_events (ts, tenant_id, actor_user_id, action, result)
			 VALUES (now(), $1, $2, $3, 'success')`, tenantID, userID, action)
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

	// A non-whitelisted hermes.tool.* action must still be rejected.
	err := insertAudit("hermes.tool.bogus_mutator")
	if err == nil {
		t.Fatalf("bogus hermes.tool.* action accepted; the whitelist is too permissive")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("bogus action error=%v want 23514 CHECK violation", err)
	}
}
