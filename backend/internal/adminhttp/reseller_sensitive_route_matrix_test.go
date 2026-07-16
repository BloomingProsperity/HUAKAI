package adminhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
)

func TestResellerProviderCredentialProbeRouteMatrix(t *testing.T) {
	routes := []struct {
		name string
		run  func(*testing.T, admin.AdminIdentity) (*httptest.ResponseRecorder, int)
	}{
		{
			name: "凭证测试",
			run: func(t *testing.T, identity admin.AdminIdentity) (*httptest.ResponseRecorder, int) {
				accounts := newProviderAccountTestAccountStoreStub()
				tester := &providerAccountProbeModelTester{}
				rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
					Auth: testerAuthStub{ident: identity}, Accounts: accounts, Tester: tester,
				}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")
				return rec, len(accounts.getArgs) + tester.calls
			},
		},
		{
			name: "上游模型探测",
			run: func(_ *testing.T, identity admin.AdminIdentity) (*httptest.ResponseRecorder, int) {
				accounts := &stubUpstreamModelsAccountStore{}
				credentials := &stubUpstreamModelsCredStore{}
				router := buildModelsRouter(UpstreamModelsDeps{
					Auth: &stubUpstreamModelsAuth{ident: identity}, Accounts: accounts, Creds: credentials,
				})
				req := httptest.NewRequest(http.MethodGet, "/99/upstream-models", nil)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				return rec, accounts.calls + credentials.calls
			},
		},
		{
			name: "批量账号修改",
			run: func(t *testing.T, identity admin.AdminIdentity) (*httptest.ResponseRecorder, int) {
				store := &stubBulkStore{}
				rec := doProviderAccountBulkPOST(t, ProviderAccountBulkDeps{
					Auth: &stubBulkAuth{ident: identity}, Store: store,
				}, map[string]any{"tag": "flaky", "enabled": false}, 10)
				return rec, len(store.updateCalls) + store.auditCount
			},
		},
		{
			name: "账号健康读取",
			run: func(t *testing.T, identity admin.AdminIdentity) (*httptest.ResponseRecorder, int) {
				store := newProviderAccountHealthStoreStub()
				rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
					Auth: providerAccountHealthAuthStub{ident: identity}, Store: store,
				}, "/admin/v1/provider-accounts/99/health")
				return rec, len(store.getArgs)
			},
		},
	}

	actors := []struct {
		name     string
		identity admin.AdminIdentity
	}{
		{"子租户 token", admintest.Reseller(701, 10)},
		{"子租户 session", admintest.ResellerSession(801, 10)},
	}
	for _, actor := range actors {
		for _, route := range routes {
			t.Run(actor.name+"/"+route.name, func(t *testing.T) {
				rec, storeCalls := route.run(t, actor.identity)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("破坏点→删除敏感 route 的 403 守卫时本断言转红：status=%d body=%s", rec.Code, rec.Body.String())
				}
				if storeCalls != 0 {
					t.Fatalf("403 后仍触达 provider account/credential store：calls=%d", storeCalls)
				}
			})
		}
	}
}
