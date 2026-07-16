package gatewayhttp

import (
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
)

func TestResellerProviderAccountRouteMatrix(t *testing.T) {
	routes := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{"列表", http.MethodGet, "/admin/v1/provider-accounts", ""},
		{"详情", http.MethodGet, "/admin/v1/provider-accounts/77", ""},
		{"创建并携带明文凭证", http.MethodPost, "/admin/v1/provider-accounts", `{"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","credentials":{"api_key":"sk-sensitive"}}`},
		{"修改", http.MethodPatch, "/admin/v1/provider-accounts/77", `{"priority":5}`},
		{"启停", http.MethodPatch, "/admin/v1/provider-accounts/77/enabled", `{"enabled":false}`},
		{"清限流", http.MethodPost, "/admin/v1/provider-accounts/77/clear-rate-limit", ""},
		{"删除", http.MethodDelete, "/admin/v1/provider-accounts/77", `{}`},
	}
	for _, actor := range resellerSensitiveActors() {
		for _, route := range routes {
			t.Run(actor.name+"/"+route.name, func(t *testing.T) {
				store := &adminPoolStoreStub{}
				credentials := &adminPoolCredentialWriterStub{}
				rec := invokeAdminPoolWithCredentialStore(t, store, credentials,
					adminPoolAuthStub{ident: actor.identity}, route.method, route.target, route.body)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("破坏点→删除 provider account 子租户守卫时本断言转红：status=%d body=%s", rec.Code, rec.Body.String())
				}
				assertResellerProviderAccountStoresUntouched(t, store, credentials)
			})
		}
	}
}

func TestResellerCredentialRouteMatrix(t *testing.T) {
	for _, actor := range resellerSensitiveActors() {
		for _, route := range adminCredentialHandlerCases() {
			t.Run(actor.name+"/凭证_"+route.name, func(t *testing.T) {
				store := &adminCredentialStoreStub{}
				audit := &adminPoolStoreStub{}
				rec := invokeAdminCredentials(t, AdminCredentialDeps{
					Auth: adminPoolAuthStub{ident: actor.identity}, Credentials: store, AuditStore: audit,
				}, route.method, route.target, route.body)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("破坏点→删除 credential route 的 403 守卫时本断言转红：status=%d body=%s", rec.Code, rec.Body.String())
				}
				assertAdminCredentialStoreUntouched(t, store, audit)
			})
		}

		t.Run(actor.name+"/续期状态", func(t *testing.T) {
			store := &adminCredentialStoreStub{}
			audit := &adminPoolStoreStub{}
			rec := invokeAdminCredentialRenewStatus(t, AdminCredentialDeps{
				Auth: adminPoolAuthStub{ident: actor.identity}, Credentials: store, AuditStore: audit,
			}, "/admin/v1/credentials/renew-status?tenant_id=10")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("破坏点→删除 renew-status 子租户守卫时本断言转红：status=%d body=%s", rec.Code, rec.Body.String())
			}
			assertAdminCredentialStoreUntouched(t, store, audit)
		})

		t.Run(actor.name+"/凭证采集", func(t *testing.T) {
			fx := newCredentialAcqHTTPFixture(t, adminPoolAuthStub{ident: actor.identity})
			rec := fx.do(t, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions",
				`{"tenant_id":10,"vendor":"openai","auth_mode":"api_key","flow_kind":"paste"}`)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("破坏点→删除 credential acquisition 平台守卫时本断言转红：status=%d body=%s", rec.Code, rec.Body.String())
			}
			fx.db.mu.Lock()
			flowCount := len(fx.db.rows)
			fx.db.mu.Unlock()
			if flowCount != 0 || len(fx.creator.inputsSnapshot()) != 0 || len(fx.adminAudit.audits) != 0 {
				t.Fatalf("403 后仍触达敏感 store：flows=%d credentials=%d audits=%d",
					flowCount, len(fx.creator.inputsSnapshot()), len(fx.adminAudit.audits))
			}
		})
	}
}

func TestResellerChannelHealthCredentialMetadataRoutesForbidden(t *testing.T) {
	for _, actor := range resellerSensitiveActors() {
		t.Run(actor.name+"/状态写入", func(t *testing.T) {
			controller := &channelHealthControllerStub{}
			rec := invokeChannelHealthAdmin(t, controller, adminPoolAuthStub{ident: actor.identity}, http.MethodPost,
				"/v1/admin/pool-accounts/101/channel-health/pause",
				`{"tenant_id":10,"vendor":"openai","account_credential_id":9001,"credential_version":1,"reason":"test"}`)
			if rec.Code != http.StatusForbidden || controller.called != "" {
				t.Fatalf("破坏点→删除 channel-health 平台守卫时本断言转红：status=%d called=%q body=%s",
					rec.Code, controller.called, rec.Body.String())
			}
		})
		t.Run(actor.name+"/元数据读取", func(t *testing.T) {
			controller := &channelHealthControllerStub{}
			rec := invokeChannelHealthReadAdmin(t, controller, adminPoolAuthStub{ident: actor.identity}, http.MethodGet,
				"/v1/admin/channel-health/summary?tenant_id=10")
			if rec.Code != http.StatusForbidden || controller.called != "" {
				t.Fatalf("破坏点→删除 channel-health 读取平台守卫时本断言转红：status=%d called=%q body=%s",
					rec.Code, controller.called, rec.Body.String())
			}
		})
	}
}

type resellerSensitiveActor struct {
	name     string
	identity admin.AdminIdentity
}

func resellerSensitiveActors() []resellerSensitiveActor {
	return []resellerSensitiveActor{
		{name: "子租户 token", identity: admintest.Reseller(501, 10)},
		{name: "子租户 session", identity: admintest.ResellerSession(601, 10)},
	}
}

func assertResellerProviderAccountStoresUntouched(t *testing.T, store *adminPoolStoreStub, credentials *adminPoolCredentialWriterStub) {
	t.Helper()
	if store.insert != nil || store.listArg != nil || store.getArg != nil || store.updateFull != nil ||
		store.update != nil || store.clear != nil || store.delete != nil || len(store.audits) != 0 || credentials.input != nil {
		t.Fatalf("403 后仍触达 provider account/credential store：store=%+v credential=%+v", store, credentials.input)
	}
}
