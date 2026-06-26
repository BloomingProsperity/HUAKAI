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

// 变异：从迁移 0084 里移除 UNIQUE(tenant_id, user_id, request_id)。
// 该测试必须变红，否则重复的用户争议就会被接受。
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

// 变异：从 ListUserCostDisputes 的 WHERE 里移除 user_id。
// 这样列表端点就会泄漏别的用户的争议；这个查询文本检查能抓到它。
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

// 变异：从 ListDisputesForAdmin 的 WHERE 里移除 tenant_id。
// 这样 admin 端点就会泄漏别的 tenant 的争议；这个查询文本检查能抓到它。
func TestCostDisputesQueryAdminListScopesTenantAndDoesNotScopeUser(t *testing.T) {
	raw, err := os.ReadFile("../../sql/queries/cost_disputes.sql")
	if err != nil {
		t.Fatalf("read query: %v", err)
	}
	sql := strings.ToLower(strings.Join(strings.Fields(string(raw)), " "))
	start := strings.Index(sql, "-- name: listdisputesforadmin :many")
	if start < 0 {
		t.Fatalf("ListDisputesForAdmin query missing; query=%s", sql)
	}
	end := strings.Index(sql[start+1:], "-- name:")
	adminSQL := sql[start:]
	if end >= 0 {
		adminSQL = sql[start : start+1+end]
	}
	if !strings.Contains(adminSQL, "where tenant_id = sqlc.arg(tenant_id)") {
		t.Fatalf("ListDisputesForAdmin must filter by tenant_id; query=%s", adminSQL)
	}
	if strings.Contains(adminSQL, "user_id = sqlc.arg(user_id)") {
		t.Fatalf("ListDisputesForAdmin must not apply user_id scope; query=%s", adminSQL)
	}
}

// 变异：在 ListDisputesForAdmin 里忽略 status_filter、limit_rows 或 offset_rows。
// admin 操作需要按状态收窄，并在 tenant 范围内的行上做稳定分页。
func TestCostDisputesQueryAdminListSupportsStatusAndPagination(t *testing.T) {
	raw, err := os.ReadFile("../../sql/queries/cost_disputes.sql")
	if err != nil {
		t.Fatalf("read query: %v", err)
	}
	sql := strings.ToLower(strings.Join(strings.Fields(string(raw)), " "))
	start := strings.Index(sql, "-- name: listdisputesforadmin :many")
	if start < 0 {
		t.Fatalf("ListDisputesForAdmin query missing; query=%s", sql)
	}
	adminSQL := sql[start:]
	if !strings.Contains(adminSQL, "sqlc.arg(status_filter)") ||
		!strings.Contains(adminSQL, "status = sqlc.arg(status_filter)") {
		t.Fatalf("ListDisputesForAdmin must apply optional status_filter; query=%s", adminSQL)
	}
	if !strings.Contains(adminSQL, "limit sqlc.arg(limit_rows)") ||
		!strings.Contains(adminSQL, "offset sqlc.arg(offset_rows)") {
		t.Fatalf("ListDisputesForAdmin must apply limit_rows and offset_rows; query=%s", adminSQL)
	}
}

// 变异：去掉 Store.CreateDispute 里对 23505 的映射。
// handler 就会返回后端 503，而不是重复对应的 409。
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

// 变异：给 sqlc 的 ListUserCostDisputes 传入零值/错误的 user_id。
// fake 记录了精确的入参，因此当从鉴权派生的 scope 丢失时测试会失败。
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

// 变异：在调用 sqlc 的 ListDisputesForAdmin 前传入零值/错误的 tenant_id、忽略 status，或没有对 limit 做封顶。
// fake 记录了精确的入参，因此当 admin 的 scope/filter/分页发生漂移时测试会失败。
func TestDisputeStoreListForAdminPassesTenantStatusAndPagination(t *testing.T) {
	q := &fakeDisputeQueries{adminListRows: []dbaudit.CostDispute{
		dbCostDispute(1, 7, 42, "req-a", DisputeStatusResolved, ""),
	}}
	store := NewCostDisputeStoreFromQueries(q)
	got, err := store.ListForAdmin(context.Background(), 7, DisputeStatusResolved, 999, 2)
	if err != nil {
		t.Fatalf("ListForAdmin: %v", err)
	}
	if q.adminListArg.TenantID != 7 ||
		q.adminListArg.StatusFilter != DisputeStatusResolved ||
		q.adminListArg.LimitRows != maxDisputeListLimit ||
		q.adminListArg.OffsetRows != 2 {
		t.Fatalf("admin list arg=%+v, want tenant=7 status=resolved capped-limit=%d offset=2",
			q.adminListArg, maxDisputeListLimit)
	}
	if len(got) != 1 || got[0].TenantID != 7 || got[0].UserID != 42 || got[0].RequestID != "req-a" {
		t.Fatalf("admin list result=%+v, want tenant 7 user 42 req-a", got)
	}
}

// 变异：在查询前接受一个未知的 status。
// 非法 filter 必须 fail closed，而不能退化成宽泛的 tenant 列表。
func TestDisputeStoreListForAdminRejectsInvalidStatus(t *testing.T) {
	q := &fakeDisputeQueries{}
	store := NewCostDisputeStoreFromQueries(q)
	_, err := store.ListForAdmin(context.Background(), 7, "settled", 25, 0)
	if !errors.Is(err, ErrDisputeInvalid) {
		t.Fatalf("ListForAdmin invalid status err=%v, want ErrDisputeInvalid", err)
	}
	if q.adminListCalled {
		t.Fatal("ListDisputesForAdmin must not run for invalid status")
	}
}

// 变异：让 ResolveDispute 保留旧的 status/operator note。
// operator 的恢复操作必须明显地推进状态，并把备注持久化下来。
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

	adminListRows   []dbaudit.CostDispute
	adminListErr    error
	adminListArg    dbaudit.ListDisputesForAdminParams
	adminListCalled bool

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

func (f *fakeDisputeQueries) ListDisputesForAdmin(_ context.Context, arg dbaudit.ListDisputesForAdminParams) ([]dbaudit.CostDispute, error) {
	f.adminListCalled = true
	f.adminListArg = arg
	return f.adminListRows, f.adminListErr
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
