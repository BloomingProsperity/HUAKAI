package adminuserhttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// 端到端验证 role 制单登录写门:挂了 AllowSessionWrite(SessionSafe) 的写端点,经 knob 开的组合解析器
// session-admin 能过鉴权;未挂的写端点对 session-admin 仍 fail-closed 拒;token 通道恒豁免。
// 共享脚手架 adminsessionauthtest 提供拟真解析器(令牌拒非 hk_admin)+ 请求助手;本包补全量非 nil 后端 fake
//(多个 handler 在鉴权【之前】做 nil 后端 503 兜底,后端为 nil 会用 503 掩盖 401 致判别失真 → 变异测不出)。

// fakeBackend 一把实现 Deps 所有后端接口,返回良性零值——仅为让 handler 越过 nil 兜底走到鉴权。
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
func (fakeBackend) Disable(context.Context, int64, int64) error                      { return nil }
func (fakeBackend) AdminClearCredentials(context.Context, int64, int64) (int, error) { return 0, nil }
func (fakeBackend) SetUserGroupForTenant(context.Context, int64, int64, string) error {
	return nil
}
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
	fb := fakeBackend{}
	r := chi.NewRouter()
	MountRoutes(r, Deps{
		Auth: adminsessionauthtest.Resolver(knob), Store: fb, SocialLinks: fb, UnlockAudit: fb,
		Unlocker: fb, Audit: fb, TwoFADisabler: fb, PasskeyResetter: fb, UserGroupSetter: fb,
		UserRemarkSetter: fb, UserStatusSetter: fb, UserCreator: fb, UserSoftDeleter: fb, SessionRevoker: fb,
	})
	return r
}

// SessionSafe 写端点:knob 开 + session-admin → 过鉴权(≠401);未挂 safe 的写端点 → fail-closed 401。
// 变异:把某 SessionSafe 路由的 .With(safe) 删掉 → 该路由 writeClassNone → session 写 401 → 首断言 RED;
//       把 safe 误挂到 token-only 路由 → 该路由不再 401 → 次断言 RED。
func TestSessionSafeRoutesOpenTokenOnlyRoutesClosed(t *testing.T) {
	h := mountForTest(true)
	sess := adminsessionauthtest.SessionBearer

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/7/unlock"},
		{http.MethodPost, "/7/2fa/force-disable"},
		{http.MethodPut, "/7/remark"},
		{http.MethodPut, "/7/status"},
		{http.MethodDelete, "/7/account-bindings/google"},
	} {
		if code := adminsessionauthtest.Status(h, tc.method, tc.path, sess); code == http.StatusUnauthorized {
			t.Fatalf("SessionSafe 写端点 %s %s 应过鉴权(≠401),得 401", tc.method, tc.path)
		}
	}

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/"},             // 建用户
		{http.MethodDelete, "/7"},          // 删用户
		{http.MethodDelete, "/7/passkeys"}, // 删 passkey
		{http.MethodPut, "/7/group"},       // 改分组(耦合计费档)
	} {
		if code := adminsessionauthtest.Status(h, tc.method, tc.path, sess); code != http.StatusUnauthorized {
			t.Fatalf("token-only 写端点 %s %s 对 session-admin 应 fail-closed 401,得 %d", tc.method, tc.path, code)
		}
	}
}

// knob 关:即便 SessionSafe 已挂,session 通道整体不走,非 hk_admin bearer 回退令牌通道被拒 401。
func TestKnobOffClosesSessionWrites(t *testing.T) {
	h := mountForTest(false)
	if code := adminsessionauthtest.Status(h, http.MethodPut, "/7/status", adminsessionauthtest.SessionBearer); code != http.StatusUnauthorized {
		t.Fatalf("knob 关时 SessionSafe 写端点也应回退令牌通道被拒 401,得 %d", code)
	}
}

// token 通道豁免:hk_admin 令牌写 token-only 端点(group)也过鉴权(≠401),不吃写分级。
func TestTokenChannelWritesTokenOnlyRoutes(t *testing.T) {
	h := mountForTest(true)
	if code := adminsessionauthtest.Status(h, http.MethodPut, "/7/group", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌写 token-only 端点应过鉴权(≠401),得 401")
	}
}
