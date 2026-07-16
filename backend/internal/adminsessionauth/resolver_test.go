package adminsessionauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// 守护组合 admin 鉴权的不变量(§14 可变异证红):
//  ① hk_admin_ 令牌恒走令牌通道(行为不变);
//  ② session 通道 deny-by-default——仅精确 role=='admin' 放行,污染/空/非 admin 一律拒;
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
	// 捕获入参,用于断言 resolver 把哪些值透传给 session 校验器(bearer / clientIP / UA)。
	gotToken string
	gotIP    string
	gotUA    string
}

func (s *stubSession) Validate(_ context.Context, token, ip, ua string) (usersession.ValidatedSession, error) {
	s.called = true
	s.gotToken = token
	s.gotIP = ip
	s.gotUA = ua
	return s.out, s.err
}

type stubIdentities struct {
	role string
	err  error
	id   *admin.AdminIdentity
}

func (s stubIdentities) ResolveActiveAdminIdentity(ctx context.Context, _ int64, userID int64) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	if s.id != nil {
		return *s.id, nil
	}
	if s.role != "admin" {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	return admin.NewAdminIdentity(ctx, admin.IdentityClaims{
		UserID: userID, Source: admin.AdminSourceSession, Role: admin.RolePlatformAdmin,
	}, nil)
}

func req(bearer string) *http.Request {
	return reqM(http.MethodGet, bearer)
}

func reqM(method, bearer string) *http.Request {
	r := httptest.NewRequest(method, "/admin/v1/api-keys", nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	return r
}

// hk_admin_ 令牌:恒走令牌通道,session 分支绝不被触碰。
func TestAdminTokenAlwaysUsesTokenPath(t *testing.T) {
	tok := &stubToken{id: admintest.Platform(7)}
	sess := &stubSession{}
	r := New(tok, sess, stubIdentities{role: "admin"}, nil)
	id, err := r.Resolve(context.Background(), req("hk_admin_ABCDEFGHIJKLMNOPQRSTUVWX"))
	if err != nil || id.TokenID != 7 {
		t.Fatalf("hk_admin 令牌应走令牌通道,得 id=%+v err=%v", id, err)
	}
	if sess.called {
		t.Fatal("hk_admin 令牌不得触碰 session 通道")
	}
}

// session + role=admin → 平台级全权 admin。
func TestSessionAdminGrantsPlatformAdmin(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{TenantID: 3, UserID: 9}}
	r := New(tok, sess, stubIdentities{role: "admin"}, nil)
	id, err := r.Resolve(context.Background(), req("valid-session"))
	if err != nil {
		t.Fatalf("admin-role session 应放行,得 err=%v", err)
	}
	if id.Role != admin.RolePlatformAdmin {
		t.Fatalf("admin-role session 应映射平台级 admin,得 role=%q", id.Role)
	}
	if id.ScopeTenantID() != 0 {
		t.Fatalf("平台级 admin 的 ScopeTenantID 应为 0(全租户),得 %d", id.ScopeTenantID())
	}
	// Source/UserID 供审计归属区分来源(P1 审计误归的正解基础)。
	// 变异:若 session 分支不打 Source=session → 归属会退回 admin_token:0 → 断言 RED。
	if id.Source != admin.AdminSourceSession {
		t.Fatalf("session-admin 的 Source 应为 session,得 %q", id.Source)
	}
	if id.UserID != 9 {
		t.Fatalf("session-admin 的 UserID 应取自 session(9),得 %d", id.UserID)
	}
	if id.AuditActor() != "admin_user:9" {
		t.Fatalf("session-admin 审计归属应为 admin_user:9,得 %q", id.AuditActor())
	}
}

func TestSessionChildKeepsTrustedSubtreeScope(t *testing.T) {
	child, err := admin.NewAdminIdentity(context.Background(), admin.IdentityClaims{
		UserID: 9, Source: admin.AdminSourceSession,
		Role: admin.RoleTenantOperator, ScopeTenantID: 10,
	}, func(context.Context, int64) ([]admin.TenantScopeNode, error) {
		return []admin.TenantScopeNode{
			{TenantID: 10, Depth: 0, ScopeRootIsChild: true},
			{TenantID: 11, Depth: 1, ScopeRootIsChild: true},
			{TenantID: 12, Depth: 2, ScopeRootIsChild: true},
		}, nil
	})
	if err != nil {
		t.Fatalf("构造子租户身份: %v", err)
	}
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{TenantID: 10, UserID: 9}}
	r := New(tok, sess, stubIdentities{id: &child}, nil)
	identity, err := r.Resolve(context.Background(), req("valid-child-session"))
	if err != nil {
		t.Fatalf("子租户 admin session 应放行: %v", err)
	}
	if identity.Role != admin.RoleTenantOperator || identity.ScopeTenantID() != 10 || identity.IsPlatformWide() {
		t.Fatalf("破坏点→resolver 恢复旧全权返回时本字段断言转红：role=%q scope=%d platform=%v",
			identity.Role, identity.ScopeTenantID(), identity.IsPlatformWide())
	}
	if err := identity.CanActOnTenant(12); err != nil {
		t.Fatalf("破坏点→session 丢失递归孙级 scope 时本断言转红：%v", err)
	}
	if err := identity.CanActOnTenant(20); !errors.Is(err, admin.ErrAdminForbidden) {
		t.Fatalf("破坏点→resolver 恢复旧全权路径时跨树断言转红：%v", err)
	}
}

// role=admin 的写方法(POST/PUT/PATCH/DELETE)未标注 SessionSafe 时经 session 通道
// 一律拒(fail-closed)——P1 的写端点隐患(审计误归 + Hermes 外键崩)
// 在灰度期物理上无法触发。
// 变异:若删掉 resolver 里的 isReadOnlyMethod gate → POST 会放行成平台级 admin → 下列断言 RED。
func TestSessionWriteMethodDeniedReadOnlyGate(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		tok := &stubToken{err: admin.ErrAdminUnauthorized}
		sess := &stubSession{out: usersession.ValidatedSession{TenantID: 3, UserID: 9}}
		r := New(tok, sess, stubIdentities{role: "admin"}, nil)
		_, err := r.Resolve(context.Background(), reqM(m, "valid-session"))
		if !errors.Is(err, admin.ErrAdminUnauthorized) {
			t.Fatalf("method=%s 经 session 通道写应被只读 gate 拒,得 err=%v", m, err)
		}
	}
	// 反向对照:同样 session 走 GET(只读)→ 放行,证明拒的是"写方法"而非"session 通道整体"。
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{TenantID: 3, UserID: 9}}
	r := New(tok, sess, stubIdentities{role: "admin"}, nil)
	if _, err := r.Resolve(context.Background(), reqM(http.MethodGet, "valid-session")); err != nil {
		t.Fatalf("GET(只读)经 session 通道应放行,得 err=%v", err)
	}
}

// deny-by-default 判别核心:非精确 'admin' 的 role 一律拒。
// 变异:若把 panelauth.PanelForRole(role)!=PanelAdmin 改成 role=="user"(反向"非 user 即 admin")
// → "administrator"/"Admin"/"" 会被误放行 → 下列断言 RED。
func TestSessionNonAdminRoleDeniedDenyByDefault(t *testing.T) {
	for _, role := range []string{"user", "", "administrator", "Admin", "ADMIN", "root", "superadmin", "  admin  "} {
		tok := &stubToken{err: admin.ErrAdminUnauthorized}
		sess := &stubSession{out: usersession.ValidatedSession{TenantID: 1, UserID: 1}}
		r := New(tok, sess, stubIdentities{role: role}, nil)
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
	r2 := New(nil, nil, nil, nil) // nil 令牌通道
	if _, err := r2.Resolve(context.Background(), req("hk_admin_x")); !errors.Is(err, admin.ErrAdminBackend) {
		t.Fatalf("nil 令牌通道应 fail-closed 返 ErrAdminBackend,得 %v", err)
	}
}

// session 无效 → 统一反枚举 ErrAdminUnauthorized(不泄露是 session 无效还是非 admin)。
func TestInvalidSessionAntiEnumeration(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{err: usersession.ErrTokenExpired}
	r := New(tok, sess, stubIdentities{role: "admin"}, nil)
	_, err := r.Resolve(context.Background(), req("expired-session"))
	if !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("无效 session 应统一返 ErrAdminUnauthorized(反枚举),得 %v", err)
	}
}

// 查角色失败 → 同样统一反枚举拒(不因存储错而误放行)。
func TestIdentityStoreErrorDenied(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{TenantID: 1, UserID: 1}}
	r := New(tok, sess, stubIdentities{err: errors.New("db down")}, nil)
	_, err := r.Resolve(context.Background(), req("valid-session"))
	if !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("查角色失败应 deny,得 %v", err)
	}
}

// ── 以下为本次加强补的判别性用例:回退分支穷举 / gate×方法矩阵 / bearer 解析边界 / clientIP 透传 ──

// hk_admin_ 令牌前缀判定优先:恒走令牌通道并透传其结果。
// 且此路径下 session 校验器绝不被触碰(前缀分支先 return)。
// 变异:把 resolver 里 `hasBearer && strings.HasPrefix(bearer,"hk_admin_")` 前缀判定删掉/改错
// → hk_admin 令牌会掉进后面的回退/ session 分支 → 令牌通道拿不到 called / 结果不对 → RED。
func TestAdminTokenPrefixPrecedesSession(t *testing.T) {
	{
		tok := &stubToken{id: admintest.Platform(42)}
		sess := &stubSession{out: usersession.ValidatedSession{UserID: 99}}
		r := New(tok, sess, stubIdentities{role: "admin"}, nil)
		id, err := r.Resolve(context.Background(), req("hk_admin_TOKENTOKENTOKENTOKEN0001"))
		if err != nil || id.TokenID != 42 {
			t.Fatalf("hk_admin 令牌应恒走令牌通道,得 id=%+v err=%v", id, err)
		}
		if !tok.called {
			t.Fatal("hk_admin 令牌应触达令牌通道")
		}
		if sess.called {
			t.Fatal("hk_admin 令牌路径绝不得触碰 session 校验器")
		}
	}
}

// hk_admin_ 令牌路径把令牌通道的错误逐字透传(不吞成别的错误码,反枚举语义由令牌通道自持)。
// 变异:若前缀分支不 `return r.token.Resolve(...)` 而落回退把错误改写 → 断言 RED。
func TestAdminTokenErrorPropagatesVerbatim(t *testing.T) {
	sentinel := errors.New("token backend blew up")
	tok := &stubToken{err: sentinel}
	r := New(tok, &stubSession{}, stubIdentities{}, nil)
	if _, err := r.Resolve(context.Background(), req("hk_admin_TOKENTOKENTOKENTOKEN0002")); !errors.Is(err, sentinel) {
		t.Fatalf("hk_admin 路径应逐字透传令牌通道错误,得 %v", err)
	}
}

// 依赖缺失(session==nil 或 roles==nil):回退令牌通道,绝不 panic。
// 变异:去掉回退条件里的 `r.session == nil ||` / `r.roles == nil ||`
// → 后面 r.session.Validate / r.roles.ActiveUserRole 对 nil 接口解引用 panic → 本测试崩(RED)。
func TestKnobOnMissingDepsFallBackToToken(t *testing.T) {
	// session == nil
	tok1 := &stubToken{err: admin.ErrAdminUnauthorized}
	r1 := New(tok1, nil, stubIdentities{role: "admin"}, nil)
	if _, err := r1.Resolve(context.Background(), req("s")); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("session==nil 应回退令牌通道,得 %v", err)
	}
	if !tok1.called {
		t.Fatal("session==nil 应委托令牌通道")
	}
	// roles == nil(session 非 nil,单独证 roles 缺失子句)
	tok2 := &stubToken{err: admin.ErrAdminUnauthorized}
	sess2 := &stubSession{out: usersession.ValidatedSession{UserID: 1}}
	r2 := New(tok2, sess2, nil, nil)
	if _, err := r2.Resolve(context.Background(), req("s")); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("roles==nil 应回退令牌通道,得 %v", err)
	}
	if sess2.called {
		t.Fatal("roles==nil 时不应进 session 校验(前置回退)")
	}
	if !tok2.called {
		t.Fatal("roles==nil 应委托令牌通道")
	}
}

// 完全无 Authorization header(!hasBearer):回退令牌通道,session 校验器绝不被调用。
// 变异:把回退条件里的 `|| !hasBearer` 删掉 → 无 bearer 会带空串进 session.Validate → sess.called 变真 → RED。
func TestKnobOnNoBearerFallsBackToToken(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{UserID: 1}}
	r := New(tok, sess, stubIdentities{role: "admin"}, nil)
	_, err := r.Resolve(context.Background(), req("")) // 无 Authorization header
	if !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("无 bearer 应回退令牌通道,得 %v", err)
	}
	if sess.called {
		t.Fatal("无 bearer 时 session 校验器绝不应被调用")
	}
	if !tok.called {
		t.Fatal("无 bearer 应委托令牌通道")
	}
}

// gate×方法完整矩阵:只读方法(GET/HEAD)放行,其余(写方法 + OPTIONS/CONNECT/TRACE/自定义)一律拒。
// 现有测试只覆盖 GET 放行 + 4 个写方法拒;此处补 HEAD 放行(文档明列的只读方法)与非写非读方法拒。
// 变异一:把 isReadOnlyMethod 里 `http.MethodHead` case 删掉 → HEAD 落 default 返 false → HEAD 被拒 → RED。
// 变异二:把 isReadOnlyMethod 的 default 改成 return true → OPTIONS/自定义方法被误放行 → RED。
func TestReadOnlyGateMethodMatrix(t *testing.T) {
	allow := []string{http.MethodGet, http.MethodHead}
	deny := []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
		http.MethodOptions, http.MethodConnect, http.MethodTrace, "PROPFIND", "get", // "get" 小写:方法名大小写敏感,不得误当只读
	}
	for _, m := range allow {
		tok := &stubToken{err: admin.ErrAdminUnauthorized}
		sess := &stubSession{out: usersession.ValidatedSession{TenantID: 3, UserID: 9}}
		r := New(tok, sess, stubIdentities{role: "admin"}, nil)
		id, err := r.Resolve(context.Background(), reqM(m, "valid-session"))
		if err != nil {
			t.Fatalf("只读方法 %q 经 session 通道应放行,得 err=%v", m, err)
		}
		if id.Role != admin.RolePlatformAdmin {
			t.Fatalf("只读方法 %q 应授平台级 admin,得 role=%q", m, id.Role)
		}
	}
	for _, m := range deny {
		tok := &stubToken{err: admin.ErrAdminUnauthorized}
		sess := &stubSession{out: usersession.ValidatedSession{TenantID: 3, UserID: 9}}
		r := New(tok, sess, stubIdentities{role: "admin"}, nil)
		if _, err := r.Resolve(context.Background(), reqM(m, "valid-session")); !errors.Is(err, admin.ErrAdminUnauthorized) {
			t.Fatalf("非只读方法 %q 经 session 通道应被 gate 拒,得 err=%v", m, err)
		}
	}
}

// clientIP 透传:配了非 nil clientip.Resolver 时,resolver 应把解析出的客户端 IP 与 UA 透传给
// session 校验器(session 反重放/异常取证依赖真实来源 IP,不能恒传空串)。
// 变异:把 resolver 里 `if r.clientIP != nil { ip = r.clientIP.ClientIP(req) }` 整块删掉
// → ip 恒为空串 → 下面 gotIP 断言 RED(证明 IP 真被求解并透传,而非 mock 掩盖)。
func TestClientIPAndUAForwardedToSession(t *testing.T) {
	cip, err := clientip.NewResolver(nil) // 无可信代理 → ClientIP 返 RemoteAddr 的 host
	if err != nil {
		t.Fatalf("构造 clientip.Resolver: %v", err)
	}
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{TenantID: 1, UserID: 1}}
	r := New(tok, sess, stubIdentities{role: "admin"}, cip)

	httpReq := httptest.NewRequest(http.MethodGet, "/admin/v1/api-keys", nil)
	httpReq.Header.Set("Authorization", "Bearer sess-abc")
	httpReq.Header.Set("User-Agent", "curl/8.0")
	httpReq.RemoteAddr = "203.0.113.7:5555"

	if _, err := r.Resolve(context.Background(), httpReq); err != nil {
		t.Fatalf("admin session GET 应放行,得 %v", err)
	}
	if sess.gotToken != "sess-abc" {
		t.Fatalf("透传给 session 的 token 应为 bearer 内容,得 %q", sess.gotToken)
	}
	if sess.gotIP != "203.0.113.7" {
		t.Fatalf("透传给 session 的 clientIP 应为解析后的 RemoteAddr host(203.0.113.7),得 %q", sess.gotIP)
	}
	if sess.gotUA != "curl/8.0" {
		t.Fatalf("透传给 session 的 UA 应为请求 UA,得 %q", sess.gotUA)
	}
}

// clientIP==nil(未配 resolver):ip 保持空串透传,不 panic(生产可能不注入 clientip)。
// 变异:把 `if r.clientIP != nil` 守卫删掉 → nil.ClientIP 虽对 nil 安全但去掉 nil 检查会改语义;
// 更关键——若把 ip 赋值改成无条件 r.clientIP.ClientIP 并当 clientIP 为 nil 时会走 nil 方法,
// 此用例证明 nil clientIP 路径不 panic 且 gotIP 为空。
func TestNilClientIPYieldsEmptyIP(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{UserID: 1}}
	r := New(tok, sess, stubIdentities{role: "admin"}, nil) // clientIP 传 nil
	if _, err := r.Resolve(context.Background(), req("sess-x")); err != nil {
		t.Fatalf("clientIP==nil 的 admin session 应放行,得 %v", err)
	}
	if sess.gotIP != "" {
		t.Fatalf("clientIP==nil 时透传 IP 应为空串,得 %q", sess.gotIP)
	}
	if sess.gotToken != "sess-x" {
		t.Fatalf("token 仍应透传,得 %q", sess.gotToken)
	}
}

// parseBearer 边界矩阵:直接对内部解析器做判别性断言(hasBearer 是否为真决定 session 分支是否可达)。
// 变异一:把 `!strings.HasPrefix(header, "Bearer ")` 判定放宽(如去掉尾空格/改 HasPrefix 为 Contains)
//
//	→ "xBearer y" / "bearer x"(小写)会被误判有 bearer → RED。
//
// 变异二:把 return 的 `tok != ""` 改成 `true` → "Bearer "(仅前缀无 token)会返回 hasBearer=true → RED。
func TestParseBearerBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		wantTok string
		wantHas bool
	}{
		{"空 header", "", "", false},
		{"无 Bearer 前缀", "sometoken", "", false},
		{"小写 bearer(大小写敏感)", "bearer tok", "", false},
		{"前缀无空格", "Bearertok", "", false},
		{"仅 Bearer 空格(空 token)", "Bearer ", "", false},
		{"仅 Bearer 无尾空格", "Bearer", "", false},
		{"Bearer 后全是空白", "Bearer     ", "", false},
		{"正常 token", "Bearer hk_x", "hk_x", true},
		{"token 前后多空格被 Trim", "Bearer    tok   ", "tok", true},
		{"token 内含空格(Trim 只削两端)", "Bearer a b", "a b", true},
		{"Basic 前缀非 Bearer", "Basic abc", "", false},
	}
	for _, c := range cases {
		tok, has := parseBearer(c.header)
		if tok != c.wantTok || has != c.wantHas {
			t.Fatalf("%s: parseBearer(%q)=(%q,%v), want (%q,%v)", c.name, c.header, tok, has, c.wantTok, c.wantHas)
		}
	}
}

// parseBearer 边界的端到端反证:header 为 "Bearer "(仅前缀空 token)时,resolver 把它当"无 bearer"
// 回退令牌通道,session 校验器绝不被调用(否则会拿空串去 Validate,可能误判)。
// 这条把 parseBearer 的 hasBearer=false 语义接到 Resolve 的回退分支上,跨函数验证。
func TestEmptyBearerTokenTreatedAsNoBearer(t *testing.T) {
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	sess := &stubSession{out: usersession.ValidatedSession{UserID: 1}}
	r := New(tok, sess, stubIdentities{role: "admin"}, nil)
	httpReq := httptest.NewRequest(http.MethodGet, "/admin/v1/api-keys", nil)
	httpReq.Header.Set("Authorization", "Bearer ") // 仅前缀,空 token
	if _, err := r.Resolve(context.Background(), httpReq); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("空 bearer token 应被当无 bearer 回退令牌通道,得 %v", err)
	}
	if sess.called {
		t.Fatal("空 bearer token 不应触达 session 校验器")
	}
}
