package main

import (
	"bytes"
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
	"github.com/BloomingProsperity/HUAKAI/internal/tenancy"
)

// newHermesGateTestDeps 构造不依赖真实数据库的最小 Hermes 路由树。
// 空查询器会让管理员认证显式返回后端不可用，从而区分管理员门和普通用户 Key 门。
func newHermesGateTestDeps(t *testing.T) *deps {
	t.Helper()
	return &deps{
		cfg:              &config.Config{BillingPolicyVersion: "test-1.0", RequestClass: "standard"},
		platformTenantID: tenancy.DefaultWorkingTenantID,
		hermesService:    hermes.NewService(&hermesAuditStoreSpy{}),
		adminAuth: adminsessionauth.New(
			admin.NewAdminResolver(nil), nil, nil, nil, tenancy.DefaultWorkingTenantID,
		),
	}
}

func TestHermes始终使用管理员门(t *testing.T) {
	d := newHermesGateTestDeps(t)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/v1/hermes/conversations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d，响应=%s，期望 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hermes_admin_backend_error") {
		t.Fatalf("响应=%s，没有经过 Hermes 管理员认证门", rec.Body.String())
	}
}

func Test旧环境变量不能恢复普通用户入口(t *testing.T) {
	t.Setenv("HUAKAI_HERMES_ADMIN_ONLY", "false")
	t.Setenv("HUAKAI_HERMES_ALLOW_LEGACY_USER_AUTH", "true")
	d := newHermesGateTestDeps(t)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/v1/hermes/tools", nil)
	req.Header.Set("Authorization", "Bearer hk_ordinary_user_key")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "hermes_admin_backend_error") {
		t.Fatalf("状态码=%d，响应=%s；旧环境变量不应恢复普通用户入口", rec.Code, rec.Body.String())
	}
}

func TestHermes拒绝请求覆盖租户且无需聊天运行器也会挂载(t *testing.T) {
	d := newHermesGateTestDeps(t)
	if d.hermesRunner != nil || d.hermesChatBridge != nil {
		t.Fatal("测试夹具不应接入聊天运行器或桥接器")
	}
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/v1/hermes/conversations?tenant_id=9", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "hermes_identity_override_forbidden") {
		t.Fatalf("状态码=%d，响应=%s；租户覆盖参数未被路由层拒绝", rec.Code, rec.Body.String())
	}
}

// TestHermes全部管理端点均经过主网关管理员门。逐个端点使用未解析的凭据请求，
// 期望统一落在管理员认证后端错误，而不是 404、普通用户鉴权或处理器本体。
func TestHermes全部管理端点均从主网关挂载(t *testing.T) {
	d := newHermesGateTestDeps(t)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "读取设置", method: http.MethodGet, path: "/v1/hermes/settings"},
		{name: "启用设置", method: http.MethodPost, path: "/v1/hermes/settings/enable", body: `{}`},
		{name: "停用设置", method: http.MethodPost, path: "/v1/hermes/settings/disable", body: `{}`},
		{name: "创建配置", method: http.MethodPost, path: "/v1/hermes/api-profiles", body: `{}`},
		{name: "列出配置", method: http.MethodGet, path: "/v1/hermes/api-profiles"},
		{name: "读取配置", method: http.MethodGet, path: "/v1/hermes/api-profiles/1"},
		{name: "轮转配置", method: http.MethodPut, path: "/v1/hermes/api-profiles/1", body: `{}`},
		{name: "删除配置", method: http.MethodDelete, path: "/v1/hermes/api-profiles/1"},
		{name: "发起聊天", method: http.MethodPost, path: "/v1/hermes/chat", body: `{}`},
		{name: "列出会话", method: http.MethodGet, path: "/v1/hermes/conversations"},
		{name: "读取会话", method: http.MethodGet, path: "/v1/hermes/conversations/1"},
		{name: "删除会话", method: http.MethodDelete, path: "/v1/hermes/conversations/1"},
		{name: "读取会话消息", method: http.MethodGet, path: "/v1/hermes/conversations/1/messages"},
		{name: "列出工具", method: http.MethodGet, path: "/v1/hermes/tools"},
		{name: "调用工具", method: http.MethodPost, path: "/v1/hermes/tool-execute", body: `{}`},
		{name: "读取上下文", method: http.MethodGet, path: "/v1/hermes/context"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "hermes_admin_backend_error") {
				t.Fatalf("%s %s 状态码=%d，响应=%s；端点未经过主网关 Hermes 管理员门", tc.method, tc.path, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHermesBoolEnabledDefaultTrue(t *testing.T) {
	for _, env := range []string{hermesMutatingEnabledEnv, hermesLLMToolLoopEnabledEnv} {
		t.Setenv(env, "")
		if value, err := hermesBoolEnabledDefaultTrue(env); err != nil || !value {
			t.Fatalf("%s 未设置时得到 value=%v err=%v，期望默认开启", env, value, err)
		}
		t.Setenv(env, "false")
		if value, err := hermesBoolEnabledDefaultTrue(env); err != nil || value {
			t.Fatalf("%s=false 时得到 value=%v err=%v", env, value, err)
		}
		t.Setenv(env, "true")
		if value, err := hermesBoolEnabledDefaultTrue(env); err != nil || !value {
			t.Fatalf("%s=true 时得到 value=%v err=%v", env, value, err)
		}
		t.Setenv(env, "非法值")
		if _, err := hermesBoolEnabledDefaultTrue(env); err == nil {
			t.Fatalf("%s 的非法布尔值未阻止启动", env)
		}
	}
}
