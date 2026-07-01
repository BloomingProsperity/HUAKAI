// Package adminsessionauthtest 为「放开 SessionSafe 写路由」的各 admin http 包测试提供共享脚手架:
// 一个 knob 可控的拟真组合解析器(令牌通道拒非 hk_admin=同生产;session→admin;roles→admin)
// + 请求/状态码助手。仅被 _test.go 引用,不进生产二进制。
//
// 各包测试仍需自备【非 nil 后端 fake】(因多数 handler 在鉴权前做 nil 后端 503 兜底,后端为 nil
// 会用 503 掩盖 401 致判别失真);本包只消除重复的解析器/请求样板。
package adminsessionauthtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// 可读的 bearer 常量:SessionBearer 非 hk_admin(走 session 通道),TokenBearer 是 hk_admin(走令牌通道)。
const (
	SessionBearer = "sess-not-hk-admin"
	TokenBearer   = "hk_admin_TOKENTOKENTOKENTOKEN0001"
)

// tokenReject 拟真生产令牌通道:仅 hk_admin_ 前缀放行,其余拒(反枚举 ErrAdminUnauthorized)。
type tokenReject struct{}

func (tokenReject) Resolve(_ context.Context, req *http.Request) (admin.AdminIdentity, error) {
	if strings.HasPrefix(req.Header.Get("Authorization"), "Bearer hk_admin_") {
		return admin.AdminIdentity{TokenID: 1, Source: admin.AdminSourceToken, Role: admin.RolePlatformAdmin}, nil
	}
	return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
}

type sessionAdmin struct{}

func (sessionAdmin) Validate(context.Context, string, string, string) (usersession.ValidatedSession, error) {
	return usersession.ValidatedSession{TenantID: 1, UserID: 42}, nil
}

type roleAdmin struct{}

func (roleAdmin) UserRole(context.Context, int64, int64) (string, error) { return "admin", nil }

// Resolver 返回 knob 可控的组合解析器:非 hk_admin bearer 在 knob 开时走 session→admin;knob 关时回退令牌通道被拒。
func Resolver(knob bool) *adminsessionauth.Resolver {
	return adminsessionauth.New(tokenReject{}, sessionAdmin{}, roleAdmin{}, nil, func() bool { return knob })
}

// Status 发一个带 bearer 的请求(body="{}" 满足多数 handler 的 JSON 解码),返回响应码。
func Status(h http.Handler, method, path, bearer string) int {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}
