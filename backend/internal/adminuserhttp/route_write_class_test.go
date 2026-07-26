package adminuserhttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// 端到端验证 role 制单登录写门:挂了 AllowSessionWrite(SessionSafe) 的写端点,经 knob 开的组合解析器
// session-admin 能过鉴权；未挂的其他包写端点仍由默认拒绝合同保护。
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
func (fakeBackend) UnlinkSocialIdentityWithAudit(context.Context, int64, int64, string, unlockAuditInput) (bool, int64, error) {
	return true, 0, nil
}
func (fakeBackend) ForceDisableTwoFAWithAudit(context.Context, int64, int64, unlockAuditInput) (int64, error) {
	return 0, nil
}
func (fakeBackend) ResetPasskeysWithAudit(context.Context, int64, int64, unlockAuditInput) (int, int64, error) {
	return 0, 0, nil
}
func (fakeBackend) SetUserGroupWithAudit(context.Context, int64, int64, string, unlockAuditInput) error {
	return nil
}
func (fakeBackend) SetUserRemarkWithAudit(context.Context, int64, int64, string, unlockAuditInput) error {
	return nil
}
func (fakeBackend) SetUserStatusWithAudit(context.Context, int64, int64, string, string, unlockAuditInput) (int64, error) {
	return 0, nil
}
func (fakeBackend) SoftDeleteUserWithAudit(context.Context, int64, int64, unlockAuditInput) (int64, error) {
	return 0, nil
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
func (fakeBackend) CreateUserWithAudit(context.Context, userCreateInput, unlockAuditInput) (userCreated, error) {
	return userCreated{}, nil
}
func mountForTest() http.Handler {
	fb := fakeBackend{}
	r := chi.NewRouter()
	MountRoutes(r, Deps{
		Auth: adminsessionauthtest.Resolver(), Store: fb, UserMutations: fb, UnlockAudit: fb,
		Unlocker: fb, Audit: fb, UserCreator: fb,
	})
	return r
}

// SessionSafe 写端点:session-admin → 过鉴权(≠401)。
// 变异:把某 SessionSafe 路由的 .With(safe) 删掉 → 该路由 writeClassNone → session 写 401 → 首断言 RED;
func TestSessionSafeRoutesOpen(t *testing.T) {
	h := mountForTest()
	sess := adminsessionauthtest.SessionBearer

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/"},
		{http.MethodDelete, "/7"},
		{http.MethodPost, "/7/unlock"},
		{http.MethodPost, "/7/2fa/force-disable"},
		{http.MethodDelete, "/7/passkeys"},
		{http.MethodPut, "/7/group"},
		{http.MethodPut, "/7/remark"},
		{http.MethodPut, "/7/status"},
		{http.MethodDelete, "/7/account-bindings/google"},
	} {
		if code := adminsessionauthtest.Status(h, tc.method, tc.path, sess); code == http.StatusUnauthorized {
			t.Fatalf("SessionSafe 写端点 %s %s 应过鉴权(≠401),得 401", tc.method, tc.path)
		}
	}
}

// token 通道保持兼容，不受浏览器会话写分级影响。
func TestTokenChannelWritesRoutes(t *testing.T) {
	h := mountForTest()
	if code := adminsessionauthtest.Status(h, http.MethodPut, "/7/group", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌写端点应过鉴权(≠401),得 401")
	}
}
