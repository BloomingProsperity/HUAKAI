package adminuserhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// 端到端验证 role 制单登录写门:挂了 AllowSessionWrite(SessionSafe) 的写端点,经 knob 开的组合解析器
// session-admin 能过鉴权;未挂的写端点对 session-admin 仍 fail-closed 拒;token 通道恒豁免。
// 用【真】adminsessionauth.Resolver + 拟真令牌 fake(拒非 hk_admin,同生产)+ 全量非 nil 后端 fake
//(关键:多个 handler 会在鉴权【之前】做 nil 后端 503 兜底,若后端为 nil 则 503 掩盖 401,判别失真;
//  故全部后端置非 nil,让每个 handler 都走到鉴权,401 与否才真实反映写分级判定 → 变异可证红)。

type fakeTokenResolver struct{}

func (fakeTokenResolver) Resolve(_ context.Context, req *http.Request) (admin.AdminIdentity, error) {
	if strings.HasPrefix(req.Header.Get("Authorization"), "Bearer hk_admin_") {
		return admin.AdminIdentity{TokenID: 1, Source: admin.AdminSourceToken, Role: admin.RolePlatformAdmin}, nil
	}
	return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
}

type fakeSession struct{}

func (fakeSession) Validate(context.Context, string, string, string) (usersession.ValidatedSession, error) {
	return usersession.ValidatedSession{TenantID: 1, UserID: 42}, nil
}

type fakeRoles struct{}

func (fakeRoles) UserRole(context.Context, int64, int64) (string, error) { return "admin", nil }

// fakeBackend 一把实现 Deps 所有后端接口,返回良性零值——仅为让 handler 越过 nil 兜底走到鉴权;
// 命中鉴权放行时被调用也返回零值不 panic(SessionSafe 路径),鉴权拒时根本不会被调用。
type fakeBackend struct{}

func (fakeBackend) AdminListUsersForTenant(context.Context, admindb.AdminListUsersForTenantParams) ([]admindb.AdminListUsersForTenantRow, error) {
	return nil, nil
}
func (fakeBackend) AdminGetUserForTenant(context.Context, admindb.AdminGetUserForTenantParams) (admindb.AdminGetUserForTenantRow, error) {
	return admindb.AdminGetUserForTenantRow{}, nil
}
func (fakeBackend) AdminGetTwoFAAdoptionStatsForTenant(context.Context, int64) (admindb.AdminGetTwoFAAdoptionStatsForTenantRow, error) {
	return admindb.AdminGetTwoFAAdoptionStatsForTenantRow{}, nil
}
func (fakeBackend) AdminListUserBalanceHistoryForTenant(context.Context, admindb.AdminListUserBalanceHistoryForTenantParams) ([]admindb.AdminListUserBalanceHistoryForTenantRow, error) {
	return nil, nil
}
func (fakeBackend) UnlinkSocialIdentity(context.Context, int64, int64, string) (bool, error) {
	return true, nil
}
func (fakeBackend) UnlockUser(context.Context, int64, int64) (userauth.User, error) {
	return userauth.User{}, nil
}
func (fakeBackend) UnlockUserWithAudit(context.Context, int64, int64, unlockAuditInput) (userauth.User, error) {
	return userauth.User{}, nil
}
func (fakeBackend) InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	return admindb.InsertAdminAuditEventRow{}, nil
}
func (fakeBackend) Disable(context.Context, int64, int64) error                     { return nil }
func (fakeBackend) AdminClearCredentials(context.Context, int64, int64) (int, error) { return 0, nil }
func (fakeBackend) SetUserGroupForTenant(context.Context, int64, int64, string) error { return nil }
func (fakeBackend) SetUserRemarkForTenant(context.Context, int64, int64, string) error {
	return nil
}
func (fakeBackend) SetUserStatusForTenant(context.Context, int64, int64, string) (int64, error) {
	return 0, nil
}
func (fakeBackend) CreateUser(context.Context, userCreateInput) (userCreated, error) {
	return userCreated{}, nil
}
func (fakeBackend) SoftDeleteForTenant(context.Context, int64, int64) (int64, error) { return 0, nil }
func (fakeBackend) Revoke(context.Context, usersession.RevokeInput) (int64, error)   { return 0, nil }

func mountForTest(knob bool) http.Handler {
	resolver := adminsessionauth.New(fakeTokenResolver{}, fakeSession{}, fakeRoles{}, nil, func() bool { return knob })
	fb := fakeBackend{}
	r := chi.NewRouter()
	MountRoutes(r, Deps{
		Auth: resolver, Store: fb, SocialLinks: fb, UnlockAudit: fb, Unlocker: fb, Audit: fb,
		TwoFADisabler: fb, PasskeyResetter: fb, UserGroupSetter: fb, UserRemarkSetter: fb,
		UserStatusSetter: fb, UserCreator: fb, UserSoftDeleter: fb, SessionRevoker: fb,
	})
	return r
}

func do(h http.Handler, method, path, bearer string) int {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// SessionSafe 写端点:knob 开 + session-admin → 过鉴权(≠401);未挂 safe 的写端点 → fail-closed 401。
// 变异:把某 SessionSafe 路由的 .With(safe) 删掉 → 该路由 writeClassNone → session 写 401 → 首断言 RED;
//       把 safe 误挂到 token-only 路由 → 该路由不再 401 → 次断言 RED。
func TestSessionSafeRoutesOpenTokenOnlyRoutesClosed(t *testing.T) {
	h := mountForTest(true)
	const sess = "sess-not-hk-admin"

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/7/unlock"},
		{http.MethodPost, "/7/2fa/force-disable"},
		{http.MethodPut, "/7/remark"},
		{http.MethodPut, "/7/status"},
		{http.MethodDelete, "/7/account-bindings/google"},
	} {
		if code := do(h, tc.method, tc.path, sess); code == http.StatusUnauthorized {
			t.Fatalf("SessionSafe 写端点 %s %s 应过鉴权(≠401),得 401", tc.method, tc.path)
		}
	}

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/"},             // 建用户
		{http.MethodDelete, "/7"},          // 删用户
		{http.MethodDelete, "/7/passkeys"}, // 删 passkey
		{http.MethodPut, "/7/group"},       // 改分组(耦合计费档)
	} {
		if code := do(h, tc.method, tc.path, sess); code != http.StatusUnauthorized {
			t.Fatalf("token-only 写端点 %s %s 对 session-admin 应 fail-closed 401,得 %d", tc.method, tc.path, code)
		}
	}
}

// knob 关:即便 SessionSafe 已挂,session 通道整体不走,非 hk_admin bearer 回退令牌通道被拒 401。
// 变异:若 resolver 漏判 knob(恒走 session)→ knob 关时 status 会过鉴权 ≠401 → RED。
func TestKnobOffClosesSessionWrites(t *testing.T) {
	h := mountForTest(false)
	if code := do(h, http.MethodPut, "/7/status", "sess-not-hk-admin"); code != http.StatusUnauthorized {
		t.Fatalf("knob 关时 SessionSafe 写端点也应回退令牌通道被拒 401,得 %d", code)
	}
}

// token 通道豁免:hk_admin 令牌写 token-only 端点(group)也过鉴权(≠401),不吃写分级。
// 变异:若把 hk_admin 前缀判定挪到写分级之后 → 令牌写 group 会撞 writeClassNone 被拒 401 → RED。
func TestTokenChannelWritesTokenOnlyRoutes(t *testing.T) {
	h := mountForTest(true)
	if code := do(h, http.MethodPut, "/7/group", "hk_admin_TOKENTOKENTOKENTOKEN0001"); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌写 token-only 端点应过鉴权(≠401),得 401")
	}
}
