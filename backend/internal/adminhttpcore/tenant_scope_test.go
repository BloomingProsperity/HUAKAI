package adminhttpcore

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// ResolveTenantScopeQuery 是三个租户作用域聚合接口共用的解析器:
// 租户管理员省略→锁本租户、显式他人→403;部署者省略/空白→400 tenant_id_required、
// 别名/非正整数→400 invalid_tenant_id、合法→放行;身份缺失→401。
// 判别:把 tenant_operator 分支改成"显式值覆盖为自身"会让越权用例从 403 变 200;
// 把空白 TrimSpace 去掉会让空白用例从 tenant_id_required 变 invalid_tenant_id。
func TestResolveTenantScopeQuery(t *testing.T) {
	operator := admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 9}
	platform := admin.AdminIdentity{Role: admin.RolePlatformAdmin}
	cases := []struct {
		name       string
		ident      admin.AdminIdentity
		target     string
		wantID     int64
		wantOK     bool
		wantStatus int
		wantCode   string
	}{
		{name: "租户管理员省略锁本租户", ident: operator, target: "/", wantID: 9, wantOK: true, wantStatus: http.StatusOK},
		{name: "租户管理员显式本租户放行", ident: operator, target: "/?tenant_id=9", wantID: 9, wantOK: true, wantStatus: http.StatusOK},
		{name: "租户管理员显式他人租户403", ident: operator, target: "/?tenant_id=7", wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "租户管理员别名400", ident: operator, target: "/?tenant_id=all", wantStatus: http.StatusBadRequest, wantCode: "invalid_tenant_id"},
		{name: "部署者合法放行", ident: platform, target: "/?tenant_id=42", wantID: 42, wantOK: true, wantStatus: http.StatusOK},
		{name: "部署者省略400", ident: platform, target: "/", wantStatus: http.StatusBadRequest, wantCode: "tenant_id_required"},
		{name: "部署者空白400", ident: platform, target: "/?tenant_id=%20%09", wantStatus: http.StatusBadRequest, wantCode: "tenant_id_required"},
		{name: "部署者零值400", ident: platform, target: "/?tenant_id=0", wantStatus: http.StatusBadRequest, wantCode: "invalid_tenant_id"},
		{name: "部署者负数400", ident: platform, target: "/?tenant_id=-3", wantStatus: http.StatusBadRequest, wantCode: "invalid_tenant_id"},
		{name: "部署者星号400", ident: platform, target: "/?tenant_id=*", wantStatus: http.StatusBadRequest, wantCode: "invalid_tenant_id"},
		{name: "部署者列表400", ident: platform, target: "/?tenant_id=1,2", wantStatus: http.StatusBadRequest, wantCode: "invalid_tenant_id"},
		{name: "无角色身份401", ident: admin.AdminIdentity{}, target: "/?tenant_id=1", wantStatus: http.StatusUnauthorized, wantCode: "admin_unauthorized"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			gotID, ok := ResolveTenantScopeQuery(rec, req, tc.ident)
			if ok != tc.wantOK || gotID != tc.wantID {
				t.Fatalf("结果=(%d,%t)，期望=(%d,%t) body=%s", gotID, ok, tc.wantID, tc.wantOK, rec.Body.String())
			}
			if tc.wantOK {
				if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
					t.Fatalf("放行时不得写响应: 状态=%d body=%s", rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("状态/错误码=%d/%s，期望=%d/%s", rec.Code, rec.Body.String(), tc.wantStatus, tc.wantCode)
			}
		})
	}
}

// ResolveTenantScopeLookup 与 ResolveTenantScopeQuery 只差一点:部署者省略 tenant_id 允许跨租户点查(返回 0)。
// 其余分支(租户管理员锁本租户 / 显式他人 403 / 别名 400 / 无角色 401)必须完全一致。
// 判别:把部署者省略分支改成 400 → 第一条用例红;把租户管理员省略改成返回 0 → 第二条用例红。
func TestResolveTenantScopeLookup(t *testing.T) {
	operator := admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 9}
	platform := admin.AdminIdentity{Role: admin.RolePlatformAdmin}
	cases := []struct {
		name       string
		ident      admin.AdminIdentity
		target     string
		wantID     int64
		wantOK     bool
		wantStatus int
		wantCode   string
	}{
		{name: "部署者省略跨租户点查", ident: platform, target: "/", wantID: 0, wantOK: true, wantStatus: http.StatusOK},
		{name: "租户管理员省略锁本租户", ident: operator, target: "/", wantID: 9, wantOK: true, wantStatus: http.StatusOK},
		{name: "租户管理员显式他人租户403", ident: operator, target: "/?tenant_id=7", wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "部署者显式合法放行", ident: platform, target: "/?tenant_id=42", wantID: 42, wantOK: true, wantStatus: http.StatusOK},
		{name: "部署者别名400", ident: platform, target: "/?tenant_id=all", wantStatus: http.StatusBadRequest, wantCode: "invalid_tenant_id"},
		{name: "部署者零值400", ident: platform, target: "/?tenant_id=0", wantStatus: http.StatusBadRequest, wantCode: "invalid_tenant_id"},
		{name: "无角色身份省略401", ident: admin.AdminIdentity{}, target: "/", wantStatus: http.StatusUnauthorized, wantCode: "admin_unauthorized"},
		{name: "无角色身份显式401", ident: admin.AdminIdentity{}, target: "/?tenant_id=1", wantStatus: http.StatusUnauthorized, wantCode: "admin_unauthorized"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			gotID, ok := ResolveTenantScopeLookup(rec, req, tc.ident)
			if ok != tc.wantOK || gotID != tc.wantID {
				t.Fatalf("结果=(%d,%t)，期望=(%d,%t) body=%s", gotID, ok, tc.wantID, tc.wantOK, rec.Body.String())
			}
			if tc.wantOK {
				if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
					t.Fatalf("放行时不得写响应: 状态=%d body=%s", rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("状态/错误码=%d/%s，期望=%d/%s", rec.Code, rec.Body.String(), tc.wantStatus, tc.wantCode)
			}
		})
	}
}
