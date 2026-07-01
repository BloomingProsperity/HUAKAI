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

// 守护组合 admin 鉴权的不变量(§14 可变异证红):
//  ① hk_admin_ 令牌恒走令牌通道(行为不变);② knob 关时一切回退令牌通道(逐字同行为);
//  ③ knob 开的 session 通道 deny-by-default——仅精确 role=='admin' 放行,污染/空/非 admin 一律拒;
//  ④ session 任何失败统一反枚举 ErrAdminUnauthorized,绝不泄露"是无效 session 还是非 admin"。

type stubToken struct {
	called bool
	id     admin.AdminIdentity
	err    error
}

func (s *stubToken) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	s.called = true
	return s.id, s.err
}

type stubSession struct {
	called bool
	out    usersession.ValidatedSession
	err    error
}

func (s *stubSession) Validate(context.Context, string, string, string) (usersession.ValidatedSession, error) {
	s.called = true
	return s.out, s.err
}

type stubRoles struct {
	role string
	err  error
}

func (s stubRoles) UserRole(context.Context, int64, int64) (string, error) {
	return s.role, s.err
}

func req(bearer string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/admin/v1/api-keys", nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	return r
}

func on() func() bool  { return func() bool { return true } }
func off() func() bool { return func() bool { return false } }

// hk_admin_ 令牌:即便 knob 开,也恒走令牌通道,session 分支绝不被触碰。
func TestAdminTokenAlwaysUsesTokenPath(t *testing.T) {
	tok := &stubToken{id: admin.AdminIdentity{TokenID: 7, Role: admin.RolePlatformAdmin}}
	sess := &stubSession{}
	r := New(tok, sess, stubRoles{role: "admin"}, nil, on())
	id, err := r.Resolve(context.Background(), req("hk_admin_ABCDEFGHIJKLMNOPQRSTUVWX"))
	if err != nil || id.TokenID != 7 {
		t.Fatalf("hk_admin 令牌应走令牌通道,得 id=%+v err=%v", id, err)
	}
	if sess.called {
		t.Fatal("hk_admin 令牌不得触碰 session 通道")
	}
}

// knob 关:session bearer 也回退令牌通道(令牌通道对非 admin bearer 返 Unauthorized),
// 且 session 校验器绝不被调用 → 与迁移前逐字同行为。
// 变异:若 Resolve 在 knob 关时仍走 session 分支 → sess.called 变真 → RED。
func TestKnobOffDelegatesToTokenAndSkipsSession(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{TenantID: 1, UserID: 2}}
	r := New(tok, sess, stubRoles{role: "admin"}, nil, off())
	_, err := r.Resolve(context.Background(), req("some-session-token"))
	if !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("knob 关应回退令牌通道结果(Unauthorized),得 %v", err)
	}
	if sess.called {
		t.Fatal("knob 关时 session 校验器绝不应被调用")
	}
	if !tok.called {
		t.Fatal("knob 关时应委托令牌通道")
	}
}

// knob 开 + session + role=admin → 平台级全权 admin。
func TestSessionAdminGrantsPlatformAdmin(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{TenantID: 3, UserID: 9}}
	r := New(tok, sess, stubRoles{role: "admin"}, nil, on())
	id, err := r.Resolve(context.Background(), req("valid-session"))
	if err != nil {
		t.Fatalf("admin-role session 应放行,得 err=%v", err)
	}
	if id.Role != admin.RolePlatformAdmin {
		t.Fatalf("admin-role session 应映射平台级 admin,得 role=%q", id.Role)
	}
	if id.ScopeTenantID != 0 {
		t.Fatalf("平台级 admin 的 ScopeTenantID 应为 0(全租户),得 %d", id.ScopeTenantID)
	}
}

// deny-by-default 判别核心:非精确 'admin' 的 role 一律拒。
// 变异:若把 panelauth.PanelForRole(role)!=PanelAdmin 改成 role=="user"(反向"非 user 即 admin")
// → "administrator"/"Admin"/"" 会被误放行 → 下列断言 RED。
func TestSessionNonAdminRoleDeniedDenyByDefault(t *testing.T) {
	for _, role := range []string{"user", "", "administrator", "Admin", "ADMIN", "root", "superadmin", "  admin  "} {
		tok := &stubToken{err: admin.ErrAdminUnauthorized}
		sess := &stubSession{out: usersession.ValidatedSession{TenantID: 1, UserID: 1}}
		r := New(tok, sess, stubRoles{role: role}, nil, on())
		_, err := r.Resolve(context.Background(), req("valid-session"))
		if !errors.Is(err, admin.ErrAdminUnauthorized) {
			t.Fatalf("role=%q 非精确 admin,必须 deny-by-default 拒,得 err=%v", role, err)
		}
	}
}

// nil 接收者 / nil 令牌通道 → fail-closed 返 ErrAdminBackend(503),绝不 panic。
// 变异:去掉 Resolve 顶部的 nil 守卫 → nil 接收者解引用 panic → 本测试崩(RED)。
func TestNilReceiverFailsClosed(t *testing.T) {
	var r *Resolver // nil
	if _, err := r.Resolve(context.Background(), req("hk_admin_x")); !errors.Is(err, admin.ErrAdminBackend) {
		t.Fatalf("nil 接收者应 fail-closed 返 ErrAdminBackend,得 %v", err)
	}
	r2 := New(nil, nil, nil, nil, on()) // nil 令牌通道
	if _, err := r2.Resolve(context.Background(), req("hk_admin_x")); !errors.Is(err, admin.ErrAdminBackend) {
		t.Fatalf("nil 令牌通道应 fail-closed 返 ErrAdminBackend,得 %v", err)
	}
}

// session 无效 → 统一反枚举 ErrAdminUnauthorized(不泄露是 session 无效还是非 admin)。
func TestInvalidSessionAntiEnumeration(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{err: usersession.ErrTokenExpired}
	r := New(tok, sess, stubRoles{role: "admin"}, nil, on())
	_, err := r.Resolve(context.Background(), req("expired-session"))
	if !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("无效 session 应统一返 ErrAdminUnauthorized(反枚举),得 %v", err)
	}
}

// 查角色失败 → 同样统一反枚举拒(不因存储错而误放行)。
func TestRoleStoreErrorDenied(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{TenantID: 1, UserID: 1}}
	r := New(tok, sess, stubRoles{err: errors.New("db down")}, nil, on())
	_, err := r.Resolve(context.Background(), req("valid-session"))
	if !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("查角色失败应 deny,得 %v", err)
	}
}
