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

// fakeStepUp 捕获入参并回放预置错误,单测 SessionStepUp 分支(不碰真密码/2FA)。
type fakeStepUp struct {
	called               bool
	gotTenant, gotUser   int64
	gotPassword, gotCode string
	ret                  error
}

func (f *fakeStepUp) VerifyStepUp(_ context.Context, tenantID, userID int64, password, code string) error {
	f.called = true
	f.gotTenant, f.gotUser, f.gotPassword, f.gotCode = tenantID, userID, password, code
	return f.ret
}

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
	r := withClass(base, SessionStepUp)
	if got := writeClassFromContext(r.Context()); got != SessionStepUp {
		t.Fatalf("挂了 SessionStepUp 中间件应读出 SessionStepUp,得 %d", got)
	}
}

// SessionSafe:session-admin 可直接写,无需 step-up(verifier 绝不被调用)。
// 变异:把 authorizeSessionWrite 的 SessionSafe case 从 return nil 改成 fallthrough/拒 → RED。
func TestSessionSafeAllowsWrite(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := adminSession(3, 9)
	su := &fakeStepUp{}
	r := New(tok, sess, stubRoles{role: "admin"}, nil, su, on())
	req := withClass(reqM(http.MethodPost, "valid-session"), SessionSafe)
	id, err := r.Resolve(req.Context(), req)
	if err != nil {
		t.Fatalf("SessionSafe 写应放行,得 err=%v", err)
	}
	if id.Role != admin.RolePlatformAdmin || id.Source != admin.AdminSourceSession || id.UserID != 9 {
		t.Fatalf("SessionSafe 写应授平台级 session-admin,得 %+v", id)
	}
	if su.called {
		t.Fatal("SessionSafe 不应触发 step-up 校验")
	}
}

// 未标注(writeClassNone)的写端点:即便 knob 开 + admin session,也 fail-closed 拒。
// 这是把爆炸半径关在默认态的核心不变量——高危端点不挂中间件即 token-only。
// 变异:把 authorizeSessionWrite 的 default 从拒改成 return nil → 未标注写被放行 → RED。
func TestUnclassifiedWriteDeniedFailClosed(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		tok := &stubToken{err: admin.ErrAdminUnauthorized}
		su := &fakeStepUp{}
		r := New(tok, adminSession(3, 9), stubRoles{role: "admin"}, nil, su, on())
		// 不挂 AllowSessionWrite 中间件 → writeClassNone。
		_, err := r.Resolve(context.Background(), reqM(m, "valid-session"))
		if !errors.Is(err, admin.ErrAdminUnauthorized) {
			t.Fatalf("method=%s 未标注写应 fail-closed 拒,得 %v", m, err)
		}
		if su.called {
			t.Fatalf("method=%s 未标注写不应触发 step-up", m)
		}
	}
}

// SessionStepUp + 有效证明:放行,且 header 载的密码/2FA 与 session 的 tenant/user 正确透传给 verifier。
// 变异一:把 resolver 读的 header 名改错 → verifier 收到空串,但本用例 verifier 返 nil 仍放行——
//
//	故额外断言 gotPassword/gotCode 精确等于 header 值,改 header 名即 RED。
//
// 变异二:把 tenant/user 传参对调 → gotTenant/gotUser 断言 RED。
func TestSessionStepUpValidProofAllows(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	su := &fakeStepUp{ret: nil}
	r := New(tok, adminSession(5, 11), stubRoles{role: "admin"}, nil, su, on())
	base := reqM(http.MethodPost, "valid-session")
	base.Header.Set(StepUpPasswordHeader, "hunter2")
	base.Header.Set(StepUpTwoFactorHeader, "998877")
	req := withClass(base, SessionStepUp)

	id, err := r.Resolve(req.Context(), req)
	if err != nil {
		t.Fatalf("SessionStepUp + 有效证明应放行,得 err=%v", err)
	}
	if id.Role != admin.RolePlatformAdmin || id.UserID != 11 {
		t.Fatalf("应授平台级 session-admin,得 %+v", id)
	}
	if !su.called {
		t.Fatal("SessionStepUp 写必须触发 step-up 校验")
	}
	if su.gotTenant != 5 || su.gotUser != 11 {
		t.Fatalf("tenant/user 应取自 session 透传,得 tenant=%d user=%d", su.gotTenant, su.gotUser)
	}
	if su.gotPassword != "hunter2" || su.gotCode != "998877" {
		t.Fatalf("证明应取自 header 透传,得 pw=%q code=%q", su.gotPassword, su.gotCode)
	}
}

// SessionStepUp:verifier 的错误(required/invalid/locked)原样传出,供 handler 映射 403/401/429。
// 变异:把 authorizeSessionWrite 的 stepUp 返回值吞掉/改写成 ErrAdminUnauthorized → errors.Is 断言 RED。
func TestSessionStepUpErrorsPropagate(t *testing.T) {
	for _, want := range []error{admin.ErrAdminStepUpRequired, admin.ErrAdminStepUpInvalid, admin.ErrAdminStepUpLocked} {
		tok := &stubToken{err: admin.ErrAdminUnauthorized}
		su := &fakeStepUp{ret: want}
		r := New(tok, adminSession(1, 2), stubRoles{role: "admin"}, nil, su, on())
		req := withClass(reqM(http.MethodDelete, "valid-session"), SessionStepUp)
		_, err := r.Resolve(req.Context(), req)
		if !errors.Is(err, want) {
			t.Fatalf("SessionStepUp 应原样传出 %v,得 %v", want, err)
		}
	}
}

// SessionStepUp 路由但未接线 verifier(stepUp==nil):fail-closed 返 ErrAdminBackend(503),绝不放行。
// 变异:把 authorizeSessionWrite 里 `if r.stepUp == nil { return ErrAdminBackend }` 删掉
// → nil.VerifyStepUp 解引用 panic(或若改成放行则越权)→ RED。
func TestSessionStepUpNilVerifierFailsClosed(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	r := New(tok, adminSession(1, 2), stubRoles{role: "admin"}, nil, nil, on()) // stepUp==nil
	req := withClass(reqM(http.MethodPost, "valid-session"), SessionStepUp)
	if _, err := r.Resolve(req.Context(), req); !errors.Is(err, admin.ErrAdminBackend) {
		t.Fatalf("SessionStepUp + nil verifier 应 fail-closed ErrAdminBackend,得 %v", err)
	}
}

// token 通道豁免(Owner Q2 定案):hk_admin_ 令牌写任何端点(含 SessionStepUp 标注)恒走令牌通道,
// 绝不被 step-up 拦(programmatic 持有即授权)。
// 变异:若把 hk_admin_ 前缀判定挪到写分级之后 → 令牌写会撞 step-up → su.called 变真 / 结果不对 → RED。
func TestTokenChannelExemptFromStepUp(t *testing.T) {
	tok := &stubToken{id: admin.AdminIdentity{TokenID: 8, Role: admin.RolePlatformAdmin, Source: admin.AdminSourceToken}}
	su := &fakeStepUp{}
	r := New(tok, &stubSession{}, stubRoles{role: "admin"}, nil, su, on())
	// 即便路由标注 SessionStepUp,hk_admin 令牌也不受二次校验约束。
	req := withClass(reqM(http.MethodPost, "hk_admin_TOKENTOKENTOKENTOKEN0009"), SessionStepUp)
	id, err := r.Resolve(req.Context(), req)
	if err != nil || id.TokenID != 8 {
		t.Fatalf("hk_admin 令牌写应恒走令牌通道并豁免 step-up,得 id=%+v err=%v", id, err)
	}
	if su.called {
		t.Fatal("token 通道绝不应触发 step-up 校验")
	}
}

// 只读方法不受写分级/step-up 影响:GET 即便标注 SessionStepUp 也直接放行,verifier 不被调用。
// 变异:若把 step-up 判定提到只读判定之前(对所有方法都验)→ GET 会调 verifier → su.called 变真 → RED。
func TestReadMethodIgnoresStepUp(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	su := &fakeStepUp{ret: admin.ErrAdminStepUpRequired} // 若被调用会致拒,反证它不该被调用
	r := New(tok, adminSession(1, 2), stubRoles{role: "admin"}, nil, su, on())
	req := withClass(reqM(http.MethodGet, "valid-session"), SessionStepUp)
	if _, err := r.Resolve(req.Context(), req); err != nil {
		t.Fatalf("只读方法应无视 step-up 直接放行,得 err=%v", err)
	}
	if su.called {
		t.Fatal("只读方法不应触发 step-up 校验")
	}
}
