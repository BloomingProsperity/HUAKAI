package adminsessionauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// withClass 跑一遍 AllowSessionWrite 中间件,返回带写分级 context 的请求(同时覆盖中间件本身)。
func withClass(r *http.Request, class AdminWriteClass) *http.Request {
	var out *http.Request
	AllowSessionWrite(class)(http.HandlerFunc(func(_ http.ResponseWriter, rr *http.Request) {
		out = rr
	})).ServeHTTP(httptest.NewRecorder(), r)
	return out
}

func adminSession(t int64, u int64) *stubSession {
	return &stubSession{out: usersession.ValidatedSession{TenantID: t, UserID: u}}
}

// 中间件把写分级塞进 context;未挂中间件时读出零值 writeClassNone(fail-closed)。
// 变异:把 AllowSessionWrite 里 WithValue 删掉 / key 改错 → 读出 writeClassNone → 下面 SessionSafe 断言 RED。
func TestAllowSessionWriteSetsContext(t *testing.T) {
	base := reqM(http.MethodPost, "s")
	if got := writeClassFromContext(base.Context()); got != writeClassNone {
		t.Fatalf("未挂中间件应读出 writeClassNone(fail-closed),得 %d", got)
	}
	r := withClass(base, SessionSafe)
	if got := writeClassFromContext(r.Context()); got != SessionSafe {
		t.Fatalf("挂了 SessionSafe 中间件应读出 SessionSafe,得 %d", got)
	}
}

// SessionSafe：session-admin 可直接写，Owner 终审不要求后端 step-up。
// 变异:把 resolver 的 `writeClassFromContext(...) != SessionSafe` 判定改错 → RED。
func TestSessionSafeAllowsWrite(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	r := newResolver(tok, adminSession(3, 9), stubRoles{role: "admin"}, nil)
	req := withClass(reqM(http.MethodPost, "valid-session"), SessionSafe)
	id, err := r.Resolve(req.Context(), req)
	if err != nil {
		t.Fatalf("SessionSafe 写应放行,得 err=%v", err)
	}
	if id.Role != admin.RoleTenantOperator || id.ScopeTenantID != 3 || id.Source != admin.AdminSourceSession || id.UserID != 9 {
		t.Fatalf("下级租户 SessionSafe 写应授本租户 tenant_operator,得 %+v", id)
	}
}

// 平台租户的 SessionSafe 写仍映射为 platform_admin，且作用域保持 0。
func TestSessionSafeAllowsPlatformWrite(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	r := newResolver(tok, adminSession(testPlatformTenantID, 10), stubRoles{role: "admin"}, nil)
	req := withClass(reqM(http.MethodPost, "valid-session"), SessionSafe)
	id, err := r.Resolve(req.Context(), req)
	if err != nil {
		t.Fatalf("平台租户 SessionSafe 写应放行,得 err=%v", err)
	}
	if id.Role != admin.RolePlatformAdmin || id.ScopeTenantID != 0 || id.UserID != 10 {
		t.Fatalf("平台租户 SessionSafe 写应授 platform_admin,得 %+v", id)
	}
}

// 未标注(writeClassNone)的写端点:即便 knob 开 + admin session,也 fail-closed 拒。
// 这是把爆炸半径关在默认态的核心不变量——高危端点不挂中间件即 token-only。
// 变异:把 resolver 写分支的默认拒改成放行(如判定恒为 SessionSafe)→ 未标注写被放行 → RED。
func TestUnclassifiedWriteDeniedFailClosed(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		tok := &stubToken{err: admin.ErrAdminUnauthorized}
		r := newResolver(tok, adminSession(3, 9), stubRoles{role: "admin"}, nil)
		// 不挂 AllowSessionWrite 中间件 → writeClassNone。
		_, err := r.Resolve(context.Background(), reqM(m, "valid-session"))
		if !errors.Is(err, admin.ErrAdminUnauthorized) {
			t.Fatalf("method=%s 未标注写应 fail-closed 拒,得 %v", m, err)
		}
	}
}

// token 通道豁免(Owner Q2 定案):hk_admin_ 令牌写任何端点恒走令牌通道,不受写分级约束。
// 这里故意让路由【不挂】SessionSafe(默认拒 session 写),证明令牌仍能写——即令牌不吃写分级那套。
// 变异:若把 hk_admin_ 前缀判定挪到写分级之后 → 令牌写会被 writeClassNone 拒 → RED。
func TestTokenChannelExemptFromWriteClass(t *testing.T) {
	tok := &stubToken{id: admin.AdminIdentity{TokenID: 8, Role: admin.RolePlatformAdmin, Source: admin.AdminSourceToken}}
	r := newResolver(tok, &stubSession{}, stubRoles{role: "admin"}, nil)
	// 未标注写分级的写请求,但带 hk_admin 令牌。
	req := reqM(http.MethodPost, "hk_admin_TOKENTOKENTOKENTOKEN0009")
	id, err := r.Resolve(req.Context(), req)
	if err != nil || id.TokenID != 8 {
		t.Fatalf("hk_admin 令牌写应恒走令牌通道、不吃写分级,得 id=%+v err=%v", id, err)
	}
}

// 只读方法不受写分级影响:GET 即便未挂 SessionSafe 也直接放行(读端点 P2a 已放开)。
// 变异:若把写分级判定的 `!isReadOnlyMethod` 前置删掉(对所有方法都判分级)→ 未标注 GET 会被拒 → RED。
func TestReadMethodIgnoresWriteClass(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	r := newResolver(tok, adminSession(testPlatformTenantID, 2), stubRoles{role: "admin"}, nil)
	req := reqM(http.MethodGet, "valid-session") // 未挂写分级
	id, err := r.Resolve(req.Context(), req)
	if err != nil {
		t.Fatalf("只读方法应无视写分级直接放行,得 err=%v", err)
	}
	if id.Role != admin.RolePlatformAdmin {
		t.Fatalf("只读 session-admin 应授平台级,得 role=%q", id.Role)
	}
}
