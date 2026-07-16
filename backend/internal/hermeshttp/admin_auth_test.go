package hermeshttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

// fakeAdminResolver 注入一个固定的身份 / 错误,替代 *admin.AdminResolver,
// 使中间件 RBAC 可在无 DB 的情况下测试。
type fakeAdminResolver struct {
	identity admin.AdminIdentity
	err      error
	called   bool
}

func (f *fakeAdminResolver) Resolve(_ context.Context, _ *http.Request) (admin.AdminIdentity, error) {
	f.called = true
	return f.identity, f.err
}

// captureNext 记录中间件注入的身份 + admin actor,使成功用例能断言贯穿传递的
// 上下文,并返回 200。
type captureNext struct {
	gotIdentity sessionauth.Identity
	gotActor    adminActor
	gotActorOK  bool
	served      bool
}

func (c *captureNext) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	c.served = true
	if id, ok := r.Context().Value(authContextKey{}).(sessionauth.Identity); ok {
		c.gotIdentity = id
	}
	c.gotActor, c.gotActorOK = adminActorFromContext(r.Context())
}

func runAdminMiddleware(resolver AdminAuthResolver, path string) (*httptest.ResponseRecorder, *captureNext) {
	next := &captureNext{}
	h := AdminAuthMiddleware(resolver)(next)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, next
}

func TestAdminAuthMiddlewareRejectsCredentialFailure(t *testing.T) {
	// 回归(变异:把 routes.go 的中间件替换还原回 APIKeyMiddleware):若某请求的
	// admin 凭证无法解析,必须被拒绝 401,且绝不应到达任何 Hermes handler。
	rec, next := runAdminMiddleware(&fakeAdminResolver{err: admin.ErrAdminUnauthorized}, "/conversations?tenant_id=7&as_user_id=42")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s want 401", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler was reached despite unauthorized admin credential")
	}
}

func TestAdminAuthMiddlewareBackendErrorIs503(t *testing.T) {
	// 回归:数据存储的瞬时故障必须 fail-closed 为 503,不能被误当成 401(那会诱使
	// 凭证枚举式的重试),也不能静默放行。
	rec, next := runAdminMiddleware(&fakeAdminResolver{err: admin.ErrAdminBackend}, "/conversations?tenant_id=7&as_user_id=42")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler reached on backend error")
	}
}

func TestAdminAuthMiddlewareNilResolverIs503(t *testing.T) {
	// 回归:未配置的 resolver 必须 fail-closed(503),绝不能在未认证状态下暴露 Hermes。
	rec, next := runAdminMiddleware(nil, "/conversations?tenant_id=7&as_user_id=42")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler reached with nil resolver")
	}
}

func TestAdminAuthMiddlewareTenantOperatorCrossTenant403(t *testing.T) {
	// 回归(变异:去掉 CanActOnTenant / scope 不匹配检查):scope 为 tenant 7 的
	// tenant_operator 请求 tenant 9 的资源时,必须被拒绝 403,且不应到达任何 handler。
	resolver := &fakeAdminResolver{identity: admintest.TenantOperator(
		100, 7)}
	rec, next := runAdminMiddleware(resolver, "/conversations?tenant_id=9&as_user_id=42")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler reached for cross-tenant operator request")
	}
}

func TestAdminAuthMiddlewarePlatformAdminRequiresTenantParam(t *testing.T) {
	// 回归:platform_admin 没有隐含 tenant;缺省 ?tenant_id 必须是 400,绝不能静默
	// 默认成某个跨 tenant 的值并泄露进 tenant 范围的 handler。
	resolver := &fakeAdminResolver{identity: admintest.Platform(
		200)}
	rec, next := runAdminMiddleware(resolver, "/conversations?as_user_id=42")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler reached for platform_admin without tenant_id")
	}
}

func TestAdminAuthMiddlewareRequiresAsUserID(t *testing.T) {
	// 回归:admin 模式要求 ?as_user_id,以便贯穿传递的 user id 能解析 users FK;
	// 缺省它必须是 400,而不是用 0 值 user id 去写入、在 DB 层违反 FK。
	resolver := &fakeAdminResolver{identity: admintest.TenantOperator(
		300, 7)}
	rec, next := runAdminMiddleware(resolver, "/conversations?tenant_id=7")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler reached without as_user_id")
	}
}

func TestAdminAuthMiddlewareOperatorSuccessInjectsScopedIdentityAndActor(t *testing.T) {
	// 回归:成功时,中间件必须贯穿传递 operator 的 scoped tenant + 请求的
	// as_user_id,并记录 operator 的 token id/role 用于审计归因。去掉 actor 注入的
	// 变异会使 gotActorOK 为 false;忽略 ScopeTenantID 的变异会改变 gotIdentity。
	resolver := &fakeAdminResolver{identity: admintest.TenantOperator(
		400, 7)}
	rec, next := runAdminMiddleware(resolver, "/conversations?as_user_id=42")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if !next.served {
		t.Fatalf("handler was not reached on authorized operator request")
	}
	if next.gotIdentity.TenantID != 7 || next.gotIdentity.UserID != 42 {
		t.Fatalf("identity=%+v want tenant=7 user=42", next.gotIdentity)
	}
	if !next.gotActorOK || next.gotActor.TokenID != 400 || next.gotActor.Role != admin.RoleTenantOperator {
		t.Fatalf("admin actor=%+v ok=%v want token=400 role=%s", next.gotActor, next.gotActorOK, admin.RoleTenantOperator)
	}
}

func TestWithAdminActorFoldsAttributionOnlyInAdminMode(t *testing.T) {
	// 回归:当且仅当上下文中存在 admin actor 时,审计 args 才应新增 admin_actor_id +
	// admin_role,使轨迹归因到真正的 operator。变异:去掉折入会让 args 缺少 operator
	// 键;在终端用户模式下折入则会错误地给正常流量打标。
	base := map[string]any{"conversation_id": int64(1002)}

	endUser := withAdminActor(context.Background(), base)
	if _, ok := endUser["admin_actor_id"]; ok {
		t.Fatalf("end-user args unexpectedly carry admin attribution: %v", endUser)
	}

	ctx := context.WithValue(context.Background(), adminActorContextKey{}, adminActor{TokenID: 77, Role: admin.RoleTenantOperator})
	adminArgs := withAdminActor(ctx, base)
	if adminArgs["admin_actor_id"] != int64(77) || adminArgs["admin_role"] != admin.RoleTenantOperator {
		t.Fatalf("admin args=%v want admin_actor_id=77 role=%s", adminArgs, admin.RoleTenantOperator)
	}
	// 原始 map 不应被改动(它在错误路径上会被复用)。
	if _, ok := base["admin_actor_id"]; ok {
		t.Fatalf("withAdminActor mutated the caller's args map: %v", base)
	}

	// 区分性:归因必须能挺过真实的持久化路径(RecordAudit 在写入前会施加
	// hermes.SanitizeArgs)。operator id 是非机密的 admin_tokens 行 PK,绝不能被脱敏。
	// 这正是先前测试漏掉的防护(它只断言了脱敏前的输出)。
	persisted := hermes.SanitizeArgs(adminArgs)
	if persisted["admin_actor_id"] != int64(77) {
		t.Fatalf("admin_actor_id did not survive SanitizeArgs: got %v (operator attribution silently dropped — key must not match the sensitive 'token' substring)", persisted["admin_actor_id"])
	}
	if persisted["admin_role"] != admin.RoleTenantOperator {
		t.Fatalf("admin_role did not survive SanitizeArgs: got %v", persisted["admin_role"])
	}
	// 证明这次重命名很重要:旧的 *_token_id 命名会被 sanitizer 脱敏,而这正是
	// 本次修复所封堵的缺陷。
	redacted := hermes.SanitizeArgs(map[string]any{"admin_actor_token_id": int64(77)})
	if redacted["admin_actor_token_id"] != "[REDACTED]" {
		t.Fatalf("expected a *_token_id key to be redacted by the sanitizer, got %v", redacted["admin_actor_token_id"])
	}
}

func TestAdminAuthMiddlewarePlatformAdminCrossTenantAllowedWithParam(t *testing.T) {
	// 回归:platform_admin 可对一个显式 tenant 进行操作;scoped tenant 必须是
	// ?tenant_id 的值,而不是 operator(缺失)的 scope。
	resolver := &fakeAdminResolver{identity: admintest.Platform(
		500)}
	rec, next := runAdminMiddleware(resolver, "/conversations?tenant_id=9&as_user_id=42")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if next.gotIdentity.TenantID != 9 || next.gotIdentity.UserID != 42 {
		t.Fatalf("identity=%+v want tenant=9 user=42", next.gotIdentity)
	}
}
