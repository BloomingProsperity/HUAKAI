package adminpoolhttp

import (
	"net/http"
	"testing"
)

// 账号出站代理绑定(proxy_binding)handler 强测试。复用既有 adminPoolStoreStub /
// invokeAdminPool / providerAccountAdmin(租户 7)。核心断言落在 store 收到的
// UpdateAdminProviderAccountParams 上(Set-flag + 互斥)。每条注明变异判别式。

// mode=direct → 清两列(Set 都 true、值 nil = 回退直连)。
// 变异:handler direct 分支漏设 SetProxyGroupID → 该断言红。
func TestAdminPoolAccounts_ProxyBindingDirect(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"proxy_binding":{"mode":"direct"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	u := store.updateFull
	if u == nil || !u.SetProxyID || u.ProxyID != nil || !u.SetProxyGroupID || u.ProxyGroupID != nil {
		t.Fatalf("direct 应清两列(SetProxyID/SetProxyGroupID=true,值 nil): %+v", u)
	}
}

// mode=proxy → 设 proxy_id + 【互斥清组】。
// 变异:删 handler "proxy 分支清组" 那行 → SetProxyGroupID=false → 红(这正是防 IP 泄漏的互斥)。
func TestAdminPoolAccounts_ProxyBindingProxyClearsGroup(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"proxy_binding":{"mode":"proxy","proxy_id":123}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	u := store.updateFull
	if u == nil || !u.SetProxyID || u.ProxyID == nil || *u.ProxyID != 123 {
		t.Fatalf("proxy 应设 proxy_id=123: %+v", u)
	}
	if !u.SetProxyGroupID || u.ProxyGroupID != nil {
		t.Fatalf("proxy 模式必须互斥清组(SetProxyGroupID=true,ProxyGroupID=nil): %+v", u)
	}
}

// mode=group → 设 proxy_group_id + 【互斥清单代理】。
// 变异:删 group 分支清单代理 → SetProxyID=false → 红。
func TestAdminPoolAccounts_ProxyBindingGroupClearsProxy(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"proxy_binding":{"mode":"group","proxy_group_id":"residential-eu"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	u := store.updateFull
	if u == nil || !u.SetProxyGroupID || u.ProxyGroupID == nil || *u.ProxyGroupID != "residential-eu" {
		t.Fatalf("group 应设 proxy_group_id: %+v", u)
	}
	if !u.SetProxyID || u.ProxyID != nil {
		t.Fatalf("group 模式必须互斥清单代理(SetProxyID=true,ProxyID=nil): %+v", u)
	}
}

// 省略 proxy_binding → 不动代理绑定(Set 都 false = COALESCE 保留旧值)。
// 变异:handler 无条件设 SetProxyID → 此处 SetProxyID=true → 红。
func TestAdminPoolAccounts_ProxyBindingOmittedKeeps(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"priority":5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	u := store.updateFull
	if u == nil || u.SetProxyID || u.SetProxyGroupID {
		t.Fatalf("省略 proxy_binding 应不动代理(Set 都 false): %+v", u)
	}
}

// mode=proxy 缺 proxy_id → 400,store 不被调用。
func TestAdminPoolAccounts_ProxyBindingProxyMissingID(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"proxy_binding":{"mode":"proxy"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
	if store.updateFull != nil {
		t.Fatalf("校验失败仍调用了 store")
	}
}

// mode=group 缺 proxy_group_id → 400。
func TestAdminPoolAccounts_ProxyBindingGroupMissingID(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"proxy_binding":{"mode":"group","proxy_group_id":"  "}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (空 group)", rec.Code)
	}
	if store.updateFull != nil {
		t.Fatalf("空 group 仍调用了 store")
	}
}

// 非法 mode → 400,store 不被调用。
func TestAdminPoolAccounts_ProxyBindingInvalidMode(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"proxy_binding":{"mode":"bogus"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
	if store.updateFull != nil {
		t.Fatalf("非法 mode 仍调用了 store")
	}
}
