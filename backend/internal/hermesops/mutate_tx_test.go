package hermesops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// --- fake pgx tx + beginner -------------------------------------------------

type fakeMutateTx struct {
	rec *txRecorder
}

type txRecorder struct {
	lockAcquired   bool
	lockKey        string
	toolCallInsert int
	adminInsert    int
	commitCount    int
	rollbackCount  int
	// injected failures
	toolCallErr error
	adminErr    error
}

type fakeBeginner struct {
	rec        *txRecorder
	beginCount int
}

func (b *fakeBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	b.beginCount++
	return &fakeMutateTx{rec: b.rec}, nil
}

func (tx *fakeMutateTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "pg_advisory_xact_lock") {
		tx.rec.lockAcquired = true
		if len(args) > 0 {
			tx.rec.lockKey, _ = args[0].(string)
		}
		return pgconn.NewCommandTag("SELECT 1"), nil
	}
	return pgconn.NewCommandTag("OK"), nil
}

func (tx *fakeMutateTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "INSERT INTO hermes_tool_calls"):
		tx.rec.toolCallInsert++
		if tx.rec.toolCallErr != nil {
			return errRow{err: tx.rec.toolCallErr}
		}
		return toolCallRow{}
	case strings.Contains(sql, "INSERT INTO admin_audit_events"):
		tx.rec.adminInsert++
		if tx.rec.adminErr != nil {
			return errRow{err: tx.rec.adminErr}
		}
		return adminAuditRow{}
	default:
		return errRow{err: errors.New("unexpected SQL in fake mutate tx: " + sql)}
	}
}

func (tx *fakeMutateTx) Commit(context.Context) error   { tx.rec.commitCount++; return nil }
func (tx *fakeMutateTx) Rollback(context.Context) error { tx.rec.rollbackCount++; return nil }
func (tx *fakeMutateTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("nested tx unused")
}
func (tx *fakeMutateTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (tx *fakeMutateTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (tx *fakeMutateTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (tx *fakeMutateTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (tx *fakeMutateTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("query unused")
}
func (tx *fakeMutateTx) Conn() *pgx.Conn { return nil }

// toolCallRow scans the InsertHermesToolCall RETURNING id, called_at.
type toolCallRow struct{}

func (toolCallRow) Scan(dest ...any) error {
	if len(dest) > 0 {
		if p, ok := dest[0].(*int64); ok {
			*p = 1
		}
	}
	return nil
}

// adminAuditRow scans the InsertAdminAuditEvent RETURNING id, occurred_at.
type adminAuditRow struct{}

func (adminAuditRow) Scan(dest ...any) error {
	if len(dest) > 0 {
		if p, ok := dest[0].(*int64); ok {
			*p = 1
		}
	}
	return nil
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// --- tests ------------------------------------------------------------------

func baseRecord() MutationAuditRecord {
	return MutationAuditRecord{
		TenantID: 7, ActorUserID: 42, AdminActorTokenID: 99,
		ToolName: ToolAccountPause, Status: ResultOK,
		AdminAction: "hermes.tool.account_pause", AdminRole: RoleTenantOperator,
		TargetType: "provider_account", TargetID: 5,
		AuditPayload: map[string]any{"account_id": int64(5)},
	}
}

func TestOrchestrator_CommitsMutationWithAuditAndLock(t *testing.T) {
	// Regression (L3+L4): a confirmed mutation commits the mutation + tool_calls
	// row + admin_audit row together, after acquiring the advisory lock. Mutation
	// check: dropping the pg_advisory_xact_lock Exec leaves lockAcquired=false;
	// dropping either audit insert drops its counter below 1.
	rec := &txRecorder{}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
	mutated := 0
	_, err := o.Execute(context.Background(), "hermes:account_toggle:7:5", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
		mutated++
		return ToolResult{Summary: map[string]any{"enabled": false}}, nil
	})
	if err != nil {
		t.Fatalf("execute err=%v want nil", err)
	}
	if !rec.lockAcquired {
		t.Fatalf("advisory lock not acquired")
	}
	if rec.lockKey != "hermes:account_toggle:7:5" {
		t.Fatalf("lock key=%q want the per-target key", rec.lockKey)
	}
	if rec.toolCallInsert != 1 || rec.adminInsert != 1 {
		t.Fatalf("audit inserts toolcall=%d admin=%d want 1/1", rec.toolCallInsert, rec.adminInsert)
	}
	if mutated != 1 {
		t.Fatalf("mutate ran %d times want 1", mutated)
	}
	if rec.commitCount != 1 || rec.rollbackCount != 0 {
		t.Fatalf("commit=%d rollback=%d want 1/0", rec.commitCount, rec.rollbackCount)
	}
}

func TestOrchestrator_AuditFailureAbortsMutation(t *testing.T) {
	// Regression (L3 / P1 fail-closed, DISCRIMINATING): if the admin_audit insert
	// fails, the mutation MUST NOT run and the tx rolls back — the account stays
	// enabled. With a best-effort (non-atomic) path the mutate callback would run
	// and the account would be paused with no audit row. Mutation check: move the
	// mutate() call BEFORE the audit inserts and `mutated` becomes 1 (RED).
	rec := &txRecorder{adminErr: errors.New("audit check violation")}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
	mutated := 0
	_, err := o.Execute(context.Background(), "lock:1", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
		mutated++
		return ToolResult{}, nil
	})
	if err == nil {
		t.Fatalf("execute err=nil want audit failure")
	}
	if mutated != 0 {
		t.Fatalf("mutation RAN despite audit failure (mutated=%d) — account would be paused with no audit row", mutated)
	}
	if rec.commitCount != 0 || rec.rollbackCount != 1 {
		t.Fatalf("commit=%d rollback=%d want 0/1 (no commit on audit failure)", rec.commitCount, rec.rollbackCount)
	}
}

func TestOrchestrator_ToolCallAuditFailureAbortsMutation(t *testing.T) {
	// Regression (L3): a tool_calls audit insert failure also aborts before the
	// mutation (the tool-call ledger is the authoritative trail). Mutation check:
	// swap the insert order so the mutation precedes the tool_calls insert and
	// `mutated` becomes 1.
	rec := &txRecorder{toolCallErr: errors.New("tool_call insert failed")}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
	mutated := 0
	_, err := o.Execute(context.Background(), "lock:2", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
		mutated++
		return ToolResult{}, nil
	})
	if err == nil || mutated != 0 {
		t.Fatalf("err=%v mutated=%d want non-nil err + mutated=0", err, mutated)
	}
	if rec.adminInsert != 0 {
		t.Fatalf("admin audit ran after tool_call failure (admin=%d) — inserts must short-circuit", rec.adminInsert)
	}
	if rec.rollbackCount != 1 {
		t.Fatalf("rollback=%d want 1", rec.rollbackCount)
	}
}

func TestOrchestrator_MutationFailureRollsBack(t *testing.T) {
	// Regression: if the mutation itself fails after the audit rows are staged,
	// the whole tx rolls back so no orphan audit row persists for a mutation that
	// did not happen. Mutation check: change the defer to commit-on-error and the
	// commitCount assertion fails.
	rec := &txRecorder{}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
	_, err := o.Execute(context.Background(), "lock:3", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
		return ToolResult{}, errors.New("mutation boom")
	})
	if err == nil {
		t.Fatalf("err=nil want mutation failure")
	}
	if rec.commitCount != 0 || rec.rollbackCount != 1 {
		t.Fatalf("commit=%d rollback=%d want 0/1 (orphan audit must roll back)", rec.commitCount, rec.rollbackCount)
	}
}

func TestOrchestrator_NilBeginnerFailsClosed(t *testing.T) {
	// Regression: a mutating tool must never proceed without the atomic audit
	// transaction. A nil beginner returns an error and never runs the mutation.
	o := NewMutateOrchestrator(nil)
	ran := false
	_, err := o.Execute(context.Background(), "lock", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
		ran = true
		return ToolResult{}, nil
	})
	if err == nil || ran {
		t.Fatalf("nil beginner: err=%v ran=%v want error + no mutation", err, ran)
	}
}
