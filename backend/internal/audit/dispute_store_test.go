package audit

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	dbaudit "github.com/BloomingProsperity/HUAKAI/internal/db/audit"
)

// Mutation: remove UNIQUE(tenant_id, user_id, request_id) from migration 0084.
// This test must go red because duplicate user disputes would otherwise be accepted.
func TestCostDisputesMigrationHasUserScopedUniqueConstraint(t *testing.T) {
	raw, err := os.ReadFile("../../sql/migrations/0084_cost_disputes.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	compact := strings.Join(strings.Fields(string(raw)), " ")
	if !strings.Contains(compact, "UNIQUE (tenant_id, user_id, request_id)") {
		t.Fatalf("migration must enforce UNIQUE(tenant_id,user_id,request_id); got:\n%s", compact)
	}
}

// Mutation: remove user_id from ListUserCostDisputes WHERE.
// The list endpoint would then leak another user's disputes; this query text check catches that.
func TestCostDisputesQueryListScopesTenantAndUser(t *testing.T) {
	raw, err := os.ReadFile("../../sql/queries/cost_disputes.sql")
	if err != nil {
		t.Fatalf("read query: %v", err)
	}
	sql := strings.ToLower(strings.Join(strings.Fields(string(raw)), " "))
	if !strings.Contains(sql, "where tenant_id = sqlc.arg(tenant_id)") ||
		!strings.Contains(sql, "and user_id = sqlc.arg(user_id)") {
		t.Fatalf("ListUserCostDisputes must filter by tenant_id and user_id; query=%s", sql)
	}
}

// Mutation: drop the 23505 mapping in Store.CreateDispute.
// The handler would return a backend 503 instead of duplicate 409.
func TestDisputeStoreMapsUserRequestDuplicate(t *testing.T) {
	q := &fakeDisputeQueries{
		createErr: &pgconn.PgError{
			Code:           "23505",
			ConstraintName: "uq_cost_disputes_tenant_user_request",
		},
	}
	store := NewCostDisputeStoreFromQueries(q)
	_, err := store.CreateDispute(context.Background(), CreateCostDisputeInput{
		TenantID: 7, UserID: 42, RequestID: "req-dup", Reason: "cost does not match receipt",
	})
	if !errors.Is(err, ErrDisputeDuplicate) {
		t.Fatalf("CreateDispute err=%v, want ErrDisputeDuplicate", err)
	}
}

// Mutation: pass zero/wrong user_id to sqlc ListUserCostDisputes.
// The fake records the exact args so the test fails when auth-derived scope is lost.
func TestDisputeStoreListUserDisputesPassesTenantAndUserScope(t *testing.T) {
	q := &fakeDisputeQueries{listRows: []dbaudit.CostDispute{
		dbCostDispute(1, 7, 42, "req-a", DisputeStatusOpen, ""),
	}}
	store := NewCostDisputeStoreFromQueries(q)
	got, err := store.ListUserDisputes(context.Background(), 7, 42, 25)
	if err != nil {
		t.Fatalf("ListUserDisputes: %v", err)
	}
	if q.listArg.TenantID != 7 || q.listArg.UserID != 42 {
		t.Fatalf("list scope=(tenant=%d,user=%d), want (7,42)", q.listArg.TenantID, q.listArg.UserID)
	}
	if len(got) != 1 || got[0].UserID != 42 || got[0].RequestID != "req-a" {
		t.Fatalf("list result=%+v, want only user 42 req-a", got)
	}
}

// Mutation: make ResolveDispute keep the old status/operator note.
// Operator recovery must visibly move state and persist the note.
func TestDisputeStoreResolveUpdatesStatusAndNote(t *testing.T) {
	resolvedAt := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	q := &fakeDisputeQueries{resolveRow: dbCostDisputeResolved(9, 7, 42, "req-r", DisputeStatusResolved, "receipt checked", resolvedAt)}
	store := NewCostDisputeStoreFromQueries(q)
	got, err := store.ResolveDispute(context.Background(), ResolveCostDisputeInput{
		TenantID: 7, ID: 9, Status: DisputeStatusResolved, OperatorNote: "receipt checked",
	})
	if err != nil {
		t.Fatalf("ResolveDispute: %v", err)
	}
	if q.resolveArg.TenantID != 7 || q.resolveArg.ID != 9 || q.resolveArg.Status != DisputeStatusResolved {
		t.Fatalf("resolve arg=%+v, want tenant=7 id=9 status=resolved", q.resolveArg)
	}
	if got.Status != DisputeStatusResolved || got.OperatorNote != "receipt checked" || got.ResolvedAt == nil {
		t.Fatalf("resolved dispute=%+v, want status/note/resolved_at", got)
	}
}

type fakeDisputeQueries struct {
	createRow dbaudit.CostDispute
	createErr error
	createArg dbaudit.CreateCostDisputeParams

	listRows []dbaudit.CostDispute
	listErr  error
	listArg  dbaudit.ListUserCostDisputesParams

	resolveRow dbaudit.CostDispute
	resolveErr error
	resolveArg dbaudit.ResolveCostDisputeParams
}

func (f *fakeDisputeQueries) CreateCostDispute(_ context.Context, arg dbaudit.CreateCostDisputeParams) (dbaudit.CostDispute, error) {
	f.createArg = arg
	return f.createRow, f.createErr
}

func (f *fakeDisputeQueries) ListUserCostDisputes(_ context.Context, arg dbaudit.ListUserCostDisputesParams) ([]dbaudit.CostDispute, error) {
	f.listArg = arg
	return f.listRows, f.listErr
}

func (f *fakeDisputeQueries) ResolveCostDispute(_ context.Context, arg dbaudit.ResolveCostDisputeParams) (dbaudit.CostDispute, error) {
	f.resolveArg = arg
	return f.resolveRow, f.resolveErr
}

func dbCostDispute(id, tenantID, userID int64, requestID, status, note string) dbaudit.CostDispute {
	return dbCostDisputeResolved(id, tenantID, userID, requestID, status, note, time.Time{})
}

func dbCostDisputeResolved(id, tenantID, userID int64, requestID, status, note string, resolvedAt time.Time) dbaudit.CostDispute {
	row := dbaudit.CostDispute{
		ID:           id,
		DisputeID:    "disp_" + requestID,
		TenantID:     tenantID,
		UserID:       userID,
		RequestID:    requestID,
		Reason:       "cost does not match receipt",
		Status:       status,
		OperatorNote: note,
		CreatedAt:    pgtype.Timestamptz{Time: time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC), Valid: true},
	}
	if !resolvedAt.IsZero() {
		row.ResolvedAt = pgtype.Timestamptz{Time: resolvedAt, Valid: true}
	}
	return row
}
