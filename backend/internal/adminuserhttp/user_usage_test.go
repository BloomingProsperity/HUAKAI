package adminuserhttp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type userUsageStoreStub struct {
	rows  []dbbilling.ListUsageRecordsRow
	err   error
	args  []dbbilling.ListUsageRecordsParams
	calls int
}

func (s *userUsageStoreStub) ListUsageRecords(_ context.Context, arg dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
	s.calls++
	s.args = append(s.args, arg)
	return s.rows, s.err
}

// 本测试守住管理员租户与 URL 用户的双作用域，并同时守住过滤参数。
// 变异：删掉 UserID、删掉 TenantID、把任一 ID 设成另一个值，或漏传
// model/provider/status/from/to，下面的精确相等断言都会变红。
func TestAdminUserUsageScopesTargetAndFilters(t *testing.T) {
	usage := &userUsageStoreStub{}
	users := &usersStoreStub{}
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)

	rec := invokeAdminUsers(t, Deps{
		Auth:       usersAuthStub{ident: tenantOperator(7)},
		Store:      users,
		UsageStore: usage,
	}, http.MethodGet, "/admin/v1/users/101/usage?model=gpt-4&provider=openai&status=error&from=2026-07-01T00:00:00Z&to=2026-07-10T00:00:00Z", nil)

	assertStatus(t, rec, http.StatusOK)
	if usage.calls != 1 || len(usage.args) != 1 {
		t.Fatalf("用量查询次数=%d，期望 1", usage.calls)
	}
	arg := usage.args[0]
	if arg.TenantID == nil || *arg.TenantID != 7 {
		t.Fatalf("TenantID=%v，期望管理员租户 7", arg.TenantID)
	}
	if arg.UserID == nil || *arg.UserID != 101 {
		t.Fatalf("UserID=%v，期望 URL 目标用户 101", arg.UserID)
	}
	if arg.Model == nil || *arg.Model != "gpt-4" {
		t.Fatalf("Model=%v，期望 gpt-4", arg.Model)
	}
	if arg.Provider == nil || *arg.Provider != "openai" {
		t.Fatalf("Provider=%v，期望 openai", arg.Provider)
	}
	if arg.Outcome == nil || *arg.Outcome != "error" {
		t.Fatalf("Outcome=%v，期望 error", arg.Outcome)
	}
	if !arg.FromTs.Valid || !arg.FromTs.Time.Equal(from) || !arg.ToTs.Valid || !arg.ToTs.Time.Equal(to) {
		t.Fatalf("时间过滤错误：FromTs=%v ToTs=%v", arg.FromTs, arg.ToTs)
	}
	if arg.APIKeyID != nil {
		t.Fatalf("APIKeyID=%v，管理端应覆盖目标用户的全部 API key", arg.APIKeyID)
	}
	if arg.PageLimit != 101 {
		t.Fatalf("PageLimit=%d，默认 limit=100 时应取 101 行判定下一页", arg.PageLimit)
	}
	if users.getArg.TenantID != 7 || users.getArg.UserID != 101 {
		t.Fatalf("用户存在性预检作用域错误：%+v", users.getArg)
	}
}

// 本测试守住 status 枚举的 fail-fast 语义。
// 变异：静默忽略非法 status、放宽枚举或先查数据库再校验，都会让状态码、
// 错误码或零调用断言变红。
func TestAdminUserUsageRejectsInvalidStatusBeforeStore(t *testing.T) {
	usage := &userUsageStoreStub{}
	users := &usersStoreStub{}

	rec := invokeAdminUsers(t, Deps{
		Auth:       usersAuthStub{ident: tenantOperator(7)},
		Store:      users,
		UsageStore: usage,
	}, http.MethodGet, "/admin/v1/users/101/usage?status=pending", nil)

	assertStatus(t, rec, http.StatusBadRequest)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, rec, &body)
	if body.Error.Code != "invalid_status" {
		t.Fatalf("错误码=%q，期望 invalid_status", body.Error.Code)
	}
	if usage.calls != 0 || users.getCalls != 0 {
		t.Fatalf("非法 status 不应触达数据库：usage=%d user=%d", usage.calls, users.getCalls)
	}
}

// 本测试守住新路由确实复用管理员鉴权，而不是只依赖 URL 中的用户 ID。
// 变异：绕过 resolveTenantIdentity 或在鉴权失败后继续执行，会触发状态码或
// 零数据库调用断言。
func TestAdminUserUsageRequiresAdminIdentity(t *testing.T) {
	usage := &userUsageStoreStub{}
	users := &usersStoreStub{}

	rec := invokeAdminUsers(t, Deps{
		Auth:       usersAuthStub{err: admin.ErrAdminUnauthorized},
		Store:      users,
		UsageStore: usage,
	}, http.MethodGet, "/admin/v1/users/101/usage", nil)

	assertStatus(t, rec, http.StatusUnauthorized)
	if usage.calls != 0 || users.getCalls != 0 {
		t.Fatalf("管理员鉴权失败后不应触达数据库：usage=%d user=%d", usage.calls, users.getCalls)
	}
}

// 本测试守住 limit+1、响应裁切及游标回传。
// 变异：查询只取 limit 行、用额外行生成游标、未解码游标或漏掉 token 映射，
// 都会命中对应断言。
func TestAdminUserUsagePaginatesAndMapsResponse(t *testing.T) {
	firstTime := time.Date(2026, 7, 10, 12, 0, 0, 123, time.UTC)
	secondTime := firstTime.Add(-time.Minute)
	usage := &userUsageStoreStub{rows: []dbbilling.ListUsageRecordsRow{
		{
			ID:                  11,
			RequestedModel:      "gpt-4",
			TokensInput:         12,
			TokensOutput:        34,
			CacheCreationTokens: 5,
			CacheReadTokens:     6,
			EndClass:            "non_streaming",
			CreatedAt:           pgtype.Timestamptz{Time: firstTime, Valid: true},
			RequestedAt:         pgtype.Timestamptz{Time: firstTime.Add(-time.Second), Valid: true},
		},
		{
			ID:             10,
			RequestedModel: "gpt-3.5",
			EndClass:       "non_streaming",
			CreatedAt:      pgtype.Timestamptz{Time: secondTime, Valid: true},
		},
	}}
	deps := Deps{
		Auth:       usersAuthStub{ident: tenantOperator(7)},
		Store:      &usersStoreStub{},
		UsageStore: usage,
	}

	rec := invokeAdminUsers(t, deps, http.MethodGet, "/admin/v1/users/101/usage?limit=1", nil)
	assertStatus(t, rec, http.StatusOK)
	var body userUsageListResponse
	decodeBody(t, rec, &body)
	if len(body.Items) != 1 || body.Items[0].RequestedModel != "gpt-4" {
		t.Fatalf("分页响应 items 错误：%+v", body.Items)
	}
	if body.Items[0].Tokens.Input != 12 || body.Items[0].Tokens.Output != 34 || body.Items[0].Tokens.CacheCreation != 5 || body.Items[0].Tokens.CacheRead != 6 {
		t.Fatalf("token 明细映射错误：%+v", body.Items[0].Tokens)
	}
	if body.NextCursor == "" {
		t.Fatal("存在额外行时 next_cursor 不应为空")
	}
	if usage.args[0].PageLimit != 2 {
		t.Fatalf("limit=1 时 PageLimit=%d，期望 2", usage.args[0].PageLimit)
	}

	rec = invokeAdminUsers(t, deps, http.MethodGet, "/admin/v1/users/101/usage?limit=1&cursor="+body.NextCursor, nil)
	assertStatus(t, rec, http.StatusOK)
	if len(usage.args) != 2 {
		t.Fatalf("游标请求后查询参数次数=%d，期望 2", len(usage.args))
	}
	cursorArg := usage.args[1]
	if !cursorArg.HasCursor || cursorArg.CursorID != 11 {
		t.Fatalf("游标参数错误：HasCursor=%v CursorID=%d", cursorArg.HasCursor, cursorArg.CursorID)
	}
	if !cursorArg.CursorCreatedAt.Valid || !cursorArg.CursorCreatedAt.Time.Equal(firstTime) {
		t.Fatalf("游标时间=%v，期望 %v", cursorArg.CursorCreatedAt, firstTime)
	}
}

// 本测试守住跨租户 ID 的反枚举行为：用户预检只能使用管理员租户，失败后不得
// 继续查询用量。变异：去掉租户预检或在 404 后继续调用 UsageStore 会变红。
func TestAdminUserUsageCrossTenantTargetReturnsNotFound(t *testing.T) {
	usage := &userUsageStoreStub{}
	users := &usersStoreStub{getErr: pgx.ErrNoRows}

	rec := invokeAdminUsers(t, Deps{
		Auth:       usersAuthStub{ident: tenantOperator(7)},
		Store:      users,
		UsageStore: usage,
	}, http.MethodGet, "/admin/v1/users/909/usage", nil)

	assertStatus(t, rec, http.StatusNotFound)
	if users.getArg.TenantID != 7 || users.getArg.UserID != 909 {
		t.Fatalf("跨租户预检参数错误：%+v", users.getArg)
	}
	if usage.calls != 0 {
		t.Fatalf("用户不属于管理员租户时仍查询了用量：calls=%d", usage.calls)
	}
}
