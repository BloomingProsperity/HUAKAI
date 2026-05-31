// debug_vars_auth_test 覆盖 P2.1：/debug/vars admin auth gate。
//
// 验证目标（Owner deep-review 2026-05-17）：
//   - 未带 Bearer → 401 admin_unauthorized，body 不含 expvar 内容
//   - resolver 未配置 → 503 admin_gate_not_configured（fail-closed）
//   - 包到 expvar 上时 handler 链构造合法，go build 不破
//
// 真实 admin token 验证 + 整条 lifecycle 走 integration_pg
// (internal/admin/issuer_integration_test.go) 验，避免本测试依赖
// 真实 *admindb.Queries / pgxpool。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// fakeAdminResolver injects a fixed identity/error so the gate's RBAC is testable
// without a real *admindb.Queries / pgxpool (S2-075 discriminating test).
type fakeAdminResolver struct {
	id  admin.AdminIdentity
	err error
}

func (f fakeAdminResolver) Resolve(_ context.Context, _ *http.Request) (admin.AdminIdentity, error) {
	return f.id, f.err
}

// 无 Bearer header → 401，且 body 不能泄漏 expvar 内容。
//
// 这里把 resolver 传成 nil 走 fail-closed 503 也是可接受的反向证据；
// 但 401 才是关键安全语义（外网客户端命中未 auth metrics 直接被拒）。
// 我们用 wrapper 链验 nil-resolver→503，再用单独 case 模拟一个总是
// 返 unauthorized 的 mini-resolver 验 401。
func TestDebugVarsAuth_NoCredentials_Returns_503_When_Resolver_Nil(t *testing.T) {
	// 不在 cmd/gateway 里启完整 main()，直接拿到与 main.go 同 package
	// 的 adminGate 包 expvar.Handler 验证 wiring 链是否正确。
	gated := adminGate(nil /* nil resolver */, expvar.Handler())

	req := httptest.NewRequest(http.MethodGet, "/debug/vars", nil)
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil resolver 必须 fail-closed 503，实际 %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Result().Body)
	defer rec.Result().Body.Close()
	if !strings.Contains(string(body), "admin_gate_not_configured") {
		t.Errorf("error body 必须含 admin_gate_not_configured，实际: %s", string(body))
	}
	// 关键安全断言：不能泄漏 expvar 内容（即不能有 cmdline / memstats 等
	// stdlib 默认变量名）。
	if strings.Contains(string(body), "cmdline") || strings.Contains(string(body), "memstats") {
		t.Errorf("503 body 泄漏 expvar 默认变量名：%s", string(body))
	}
}

// S2-075: a tenant_operator is AUTHENTICATED but must NOT be AUTHORIZED to read
// the process-global /debug/vars metrics — only platform_admin may. This is the
// discriminating pair: same gate + handler, tenant denied (403, no expvar leak)
// vs platform allowed (200, reaches expvar). Mutation check: drop the role check
// in adminGate and the tenant case flips to 200 + leaks "memstats" → red.
func TestDebugVarsAuth_TenantOperator_Forbidden_NoLeak(t *testing.T) {
	resolver := fakeAdminResolver{id: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 42}}
	gated := adminGate(resolver, expvar.Handler())

	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/vars", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant_operator 必须 403 forbidden（已认证但未授权），实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "admin_forbidden_scope") {
		t.Errorf("body 必须含 admin_forbidden_scope，实际: %s", body)
	}
	// 关键：被拒时绝不能泄漏任何 expvar 全局指标内容。
	if strings.Contains(body, "memstats") || strings.Contains(body, "cmdline") {
		t.Errorf("403 仍泄漏了 expvar 全局指标内容：%s", body)
	}
}

// S2-075 positive half: platform_admin reaches the metrics (proves the gate is
// not just blanket-denying — it discriminates by role).
func TestDebugVarsAuth_PlatformAdmin_ReachesMetrics(t *testing.T) {
	resolver := fakeAdminResolver{id: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}
	gated := adminGate(resolver, expvar.Handler())

	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/vars", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("platform_admin 必须 200 命中 metrics，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "memstats") {
		t.Errorf("platform_admin 应能读到 expvar 内容（memstats），实际 body 头: %.120s", rec.Body.String())
	}
}

// A resolver error (invalid/missing credential) still yields 401 — not 403/200.
func TestDebugVarsAuth_ResolverError_Returns401(t *testing.T) {
	resolver := fakeAdminResolver{err: errors.New("unauthorized")}
	gated := adminGate(resolver, expvar.Handler())

	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/vars", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("resolver error 必须 401，实际 %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "memstats") {
		t.Errorf("401 不得泄漏 expvar 内容")
	}
}

// 验证 error body 是合法 JSON，前端 / curl ops 友好。
func TestDebugVarsAuth_ErrorBodyIsJSON(t *testing.T) {
	gated := adminGate(nil, expvar.Handler())

	req := httptest.NewRequest(http.MethodGet, "/debug/vars", nil)
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type 应是 application/json，实际 %q", got)
	}
	body, _ := io.ReadAll(rec.Result().Body)
	defer rec.Result().Body.Close()
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("error body 必须是合法 JSON，实际 %q err=%v", string(body), err)
	}
	if parsed.Error.Code == "" {
		t.Errorf("error.code 必须非空")
	}
}

// writeAdminGateError 输出格式快照：状态码 + Content-Type + 字段结构。
// 防 main.go 改 writeAdminGateError 时静默破坏前端假设。
func TestWriteAdminGateError_OutputShape(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		code    string
		message string
	}{
		{"unauthorized", http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential"},
		{"backend_error", http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure"},
		{"not_configured", http.StatusServiceUnavailable, "admin_gate_not_configured", "admin auth resolver unset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeAdminGateError(rec, tc.status, tc.code, tc.message)
			if rec.Code != tc.status {
				t.Errorf("状态码不一致：want=%d got=%d", tc.status, rec.Code)
			}
			if rec.Header().Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type 必须 application/json")
			}
			body, _ := io.ReadAll(rec.Result().Body)
			defer rec.Result().Body.Close()
			var parsed struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("非法 JSON: %v body=%q", err, string(body))
			}
			if parsed.Error.Code != tc.code {
				t.Errorf("error.code: want %q got %q", tc.code, parsed.Error.Code)
			}
			if parsed.Error.Message != tc.message {
				t.Errorf("error.message: want %q got %q", tc.message, parsed.Error.Message)
			}
		})
	}
}

// TestWriteAdminGateErrorProducesValidJSONForControlChars guards S2-148 for the admin-gate error
// writer. Today it is only called with static literals, but it shared the fmt %q hand-formatter,
// so this locks the writer itself against re-introducing the invalid-JSON anti-pattern.
// Mutation check: restore the fmt %q formatter and json.Valid goes false on the \x01 byte → red.
func TestWriteAdminGateErrorProducesValidJSONForControlChars(t *testing.T) {
	rec := httptest.NewRecorder()
	msg := "missing or invalid admin credential \x01 \"x\"\nline2"
	writeAdminGateError(rec, http.StatusUnauthorized, "admin_unauthorized", msg)

	body := rec.Body.Bytes()
	if !json.Valid(body) {
		t.Fatalf("admin-gate error body must be valid JSON even with control chars; got %q", body)
	}
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal admin-gate error body: %v; body=%q", err, body)
	}
	if parsed.Error.Code != "admin_unauthorized" || parsed.Error.Message != msg {
		t.Fatalf("code/message must round-trip; got code=%q message=%q", parsed.Error.Code, parsed.Error.Message)
	}
}
