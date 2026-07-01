package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

// newHermesGateTestDeps 构建一个 deps:其中 hermesService 与 hermesRunner
// 均非 nil(以满足 /v1/hermes 的挂载条件),外加一个
// queries 为 nil 的 admin.AdminResolver。queries 为 nil 的 resolver 在 Resolve 时
// 返回 ErrAdminBackend,admin 中间件会把它映射为带有独特错误码的 503——
// 从而让进程内测试无需数据库即可把 admin 挂载路径
// 与旧版路径区分开来。
func newHermesGateTestDeps(t *testing.T, adminOnly bool) *deps {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	runner, err := hermes.NewRunnerClient(hermes.RunnerConfig{
		RunnerURL:     "http://runner.local",
		JWTPrivateKey: privateKey,
		JWTKID:        "kid-test",
		HTTPClient:    &http.Client{},
	})
	if err != nil {
		t.Fatalf("NewRunnerClient: %v", err)
	}
	return &deps{
		cfg:             &config.Config{BillingPolicyVersion: "test-1.0", RequestClass: "standard"},
		hermesService:   hermes.NewService(&hermesAuditStoreSpy{}),
		hermesRunner:    runner,
		hermesAdminOnly: adminOnly,
		// queries 为 nil -> Resolve 返回 ErrAdminBackend(fail-closed 503)。
		// 组合器包一层,knob=nil(session 通道关)→ 委托令牌通道,行为不变。
		adminAuth: adminsessionauth.New(admin.NewAdminResolver(nil), nil, nil, nil, nil),
		// inboundAuth 故意为 nil:旧版 APIKeyMiddleware 会把 nil 的
		// resolver 映射为 503 hermes_auth_unavailable,与 admin 的错误码有区别。
	}
}

func TestHermesAdminOnlyModeUsesAdminGate(t *testing.T) {
	// 回归(变异:把 routes.go 退回成始终使用 APIKeyMiddleware):在
	// admin-only 模式下,对 Hermes 端点的无凭证请求由
	// ADMIN 中间件处理。在 queries 为 nil 的 admin resolver 下,它 fail closed 成
	// 503 hermes_admin_backend_error——一个旧版路径永不产出的错误码——这就
	// 证明挂载的是 admin 中间件(而非客户密钥中间件)。
	d := newHermesGateTestDeps(t, true)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/v1/hermes/conversations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hermes_admin_backend_error") {
		t.Fatalf("body=%s want hermes_admin_backend_error (admin middleware mounted)", rec.Body.String())
	}
}

func TestHermesAdminOnlyFalsePreservesLegacyEndUserPath(t *testing.T) {
	// 回归(回滚路径):当 HUAKAI_HERMES_ADMIN_ONLY=false 时,旧版的
	// 客户密钥 APIKeyMiddleware 被原封不动地挂载。无凭证请求
	// 由「那个」中间件处理——其客户密钥 resolver 路径产出
	// 旧版的 hermes_auth_backend_error 错误码,它与 admin
	// 中间件的 hermes_admin_backend_error 有区别,从而证明回滚路径
	// 完好。(变异:若 routes.go 忽略该开关而始终挂载
	// admin 中间件,则此处 body 会改而携带 admin 的错误码。)
	d := newHermesGateTestDeps(t, false)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/v1/hermes/conversations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hermes_auth_backend_error") {
		t.Fatalf("body=%s want hermes_auth_backend_error (legacy middleware mounted)", body)
	}
	if strings.Contains(body, "hermes_admin_") {
		t.Fatalf("body=%s unexpectedly carries an admin-path code in legacy mode", body)
	}
}

func TestHermesAdminOnlyFromEnvFailClosed(t *testing.T) {
	// S10:resolver 是 fail-closed 的。默认(未设置)与格式错误的值
	// 行为同前;此次安全变更在于:仅有 HUAKAI_HERMES_ADMIN_ONLY=false
	// 本身,不再降级到旧版客户密钥认证——它要求显式的
	// 第二个 opt-in,且在生产环境下被拒绝。

	// 默认未设置 -> admin-only。变异:把默认改为 false -> 红。
	t.Setenv(hermesAdminOnlyEnv, "")
	t.Setenv(hermesAllowLegacyUserAuthEnv, "")
	if v, err := hermesAdminOnlyFromEnv(nil); err != nil || !v {
		t.Fatalf("unset v=%v err=%v want true,nil", v, err)
	}

	// =false 但「没有」第二个 opt-in -> 仍是 admin-only(fail-closed 的核心)。
	// 变异:去掉 opt-in 要求(直接 return ParseBool(raw))-> v=false -> 红。
	t.Setenv(hermesAdminOnlyEnv, "false")
	t.Setenv(hermesAllowLegacyUserAuthEnv, "")
	if v, err := hermesAdminOnlyFromEnv(nil); err != nil || !v {
		t.Fatalf("=false without opt-in v=%v err=%v want true,nil (fail-closed)", v, err)
	}
	// 一个为真但不等于 "true" 的 opt-in 值不算数。
	t.Setenv(hermesAllowLegacyUserAuthEnv, "1")
	if v, err := hermesAdminOnlyFromEnv(nil); err != nil || !v {
		t.Fatalf("opt-in must be exactly true; got v=%v err=%v want true,nil", v, err)
	}

	// =false 且「带有」第二个 opt-in(非生产)-> 旧版模式(回滚可用)。
	// 变异:去掉「两个 opt-in 都满足」的分支 -> 永远到不了 false -> 红。
	t.Setenv(hermesAllowLegacyUserAuthEnv, "true")
	if v, err := hermesAdminOnlyFromEnv(nil); err != nil || v {
		t.Fatalf("both opt-ins v=%v err=%v want false,nil (legacy enabled)", v, err)
	}

	// 两个 opt-in 都满足 + 生产 -> 启动拒绝。变异:删除生产环境的
	// 拒绝逻辑 -> err 为 nil -> 红。
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	if _, err := hermesAdminOnlyFromEnv(nil); err == nil {
		t.Fatalf("legacy mode in production must be a boot error, got nil")
	}
	t.Setenv("HUAKAI_RELEASE_MODE", "")

	// 格式错误的值 -> fail-loud 启动报错(行为不变)。
	t.Setenv(hermesAdminOnlyEnv, "not-a-bool")
	if _, err := hermesAdminOnlyFromEnv(nil); err == nil {
		t.Fatalf("malformed value must be a boot error, got nil")
	}
}

func TestHermesBoolEnabledDefaultTrue(t *testing.T) {
	// 本测试要抓的缺陷:两个运行时 kill-switch 解析器(KNOB A
	// HUAKAI_HERMES_MUTATING_ENABLED、KNOB B HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED)
	// 必须「默认为 TRUE」(未设置 => 启用 => 零行为变化),尊重显式的
	// bool,并对格式错误的值「fail loud」(绝不静默禁用强制,
	// 也绝不静默重新启用运维本想关掉的面)。
	for _, env := range []string{hermesMutatingEnabledEnv, hermesLLMToolLoopEnabledEnv} {
		// 未设置 -> true。变异:把空串的返回值改成 false -> 红。
		t.Setenv(env, "")
		if v, err := hermesBoolEnabledDefaultTrue(env); err != nil || !v {
			t.Fatalf("%s unset v=%v err=%v want true,nil (default-enabled)", env, v, err)
		}
		// 尊重显式的 false。变异:硬编码 return true -> 红。
		t.Setenv(env, "false")
		if v, err := hermesBoolEnabledDefaultTrue(env); err != nil || v {
			t.Fatalf("%s=false v=%v err=%v want false,nil", env, v, err)
		}
		// 尊重显式的 true。
		t.Setenv(env, "true")
		if v, err := hermesBoolEnabledDefaultTrue(env); err != nil || !v {
			t.Fatalf("%s=true v=%v err=%v want true,nil", env, v, err)
		}
		// 格式错误 -> fail-loud 启动报错。变异:吞掉 ParseBool 的错误并
		// 返回一个默认值 -> err 为 nil -> 红。
		t.Setenv(env, "maybe")
		if _, err := hermesBoolEnabledDefaultTrue(env); err == nil {
			t.Fatalf("%s malformed must be a boot error, got nil", env)
		}
		t.Setenv(env, "")
	}
}
