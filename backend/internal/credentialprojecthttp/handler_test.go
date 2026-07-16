package credentialprojecthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type authStub struct {
	identity admin.AdminIdentity
}

func (s authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.identity, nil
}

type storeStub struct {
	record        credentialstore.CredentialRecord
	loadedPayload []byte
	savedPayload  []byte
	saveArgument  []byte
	loadCalls     int
	saveCalls     int
}

func (s *storeStub) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	s.loadCalls++
	record := s.record
	record.PlaintextPayload = append([]byte(nil), s.record.PlaintextPayload...)
	s.loadedPayload = record.PlaintextPayload
	return record, nil
}

func (s *storeStub) SaveRefreshSuccess(_ context.Context, _ credentialstore.CredentialRecord, payload []byte, _ time.Time, _ string) error {
	s.saveCalls++
	s.saveArgument = payload
	s.savedPayload = append([]byte(nil), payload...)
	return nil
}

func (s *storeStub) materializedProject(t *testing.T) string {
	t.Helper()
	handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth)
	if err != nil {
		t.Fatalf("查找凭证 handler 失败：%v", err)
	}
	material, err := handler.RuntimeMaterial(s.savedPayload)
	if err != nil {
		t.Fatalf("物化写回凭证失败：%v", err)
	}
	return material.Extra["project_id"]
}

type resolverStub struct {
	projectRef string
	err        error
	calls      int
	token      string
}

func (s *resolverStub) ResolveProjectID(_ context.Context, token string) (string, error) {
	s.calls++
	s.token = token
	return s.projectRef, s.err
}

type auditStub struct {
	params []admindb.InsertAdminAuditEventParams
}

type failureEnricherStub struct {
	payload []byte
}

func (s *failureEnricherStub) Enrich(context.Context, string, []byte) (projectenrich.Result, error) {
	s.payload = []byte(`{"access_token":"copied-secret","project_metadata_status":"operator_attention"}`)
	return projectenrich.Result{Payload: s.payload, Attempted: true}, errors.New("上游暂不可用")
}

func (s *auditStub) InsertAdminAuditEvent(_ context.Context, params admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	s.params = append(s.params, params)
	return admindb.InsertAdminAuditEventRow{}, nil
}

func TestResolveProjectWritesBackMaterializedProjectWithoutTokenLeak(t *testing.T) {
	store := &storeStub{record: credentialstore.CredentialRecord{
		ID: 201, TenantID: 7, ProviderAccountID: 77,
		Vendor: credentialstore.VendorAntigravity, AuthMode: credentialstore.AuthModeOAuth,
		State: credentialstore.StateActive, CredentialVersion: 3,
		PlaintextPayload: []byte(`{"access_token":"access-secret-never-return","refresh_token":"refresh-secret-never-return"}`),
	}}
	resolver := &resolverStub{projectRef: "project-manual"}
	audit := &auditStub{}
	handler := mountedHandler(Deps{
		Auth:  authStub{identity: admintest.Platform(9)},
		Store: store, Enricher: projectenrich.New(resolver), Audit: audit,
	})

	request := httptest.NewRequest(http.MethodPost, "/provider-accounts/77/credentials/201/resolve-project", strings.NewReader(`{"tenant_id":7,"reason":"人工修复"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d，期望 200，body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "access_token") || strings.Contains(response.Body.String(), "access-secret") || strings.Contains(response.Body.String(), "refresh-secret") {
		t.Fatalf("响应泄漏凭据材料：%s", response.Body.String())
	}
	var body resolveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if body.ProjectRef != "project-manual" || store.materializedProject(t) != "project-manual" {
		t.Fatalf("project 未写回并物化：body=%+v payload=%s", body, store.savedPayload)
	}
	if resolver.calls != 1 || resolver.token != "access-secret-never-return" || store.saveCalls != 1 {
		t.Fatalf("解析/保存调用不符：resolver=%+v saveCalls=%d", resolver, store.saveCalls)
	}
	if len(audit.params) != 1 || audit.params[0].Action != auditAction || audit.params[0].RequestID == nil || strings.TrimSpace(*audit.params[0].RequestID) == "" {
		t.Fatalf("审计未带动作与 correlation：%+v", audit.params)
	}
	if strings.Contains(string(audit.params[0].Payload), "access-secret") || strings.Contains(string(audit.params[0].Payload), "refresh-secret") {
		t.Fatalf("审计泄漏凭据材料：%s", audit.params[0].Payload)
	}
	assertZeroized(t, store.loadedPayload, "解密载荷")
	assertZeroized(t, store.saveArgument, "写回载荷")
}

func TestResolveProjectRejectsTamperedTenant(t *testing.T) {
	store := &storeStub{record: credentialstore.CredentialRecord{
		ID: 201, TenantID: 7, ProviderAccountID: 77,
		Vendor: credentialstore.VendorAntigravity, AuthMode: credentialstore.AuthModeOAuth,
		PlaintextPayload: []byte(`{"access_token":"access-secret"}`),
	}}
	resolver := &resolverStub{projectRef: "must-not-resolve"}
	handler := mountedHandler(Deps{
		Auth:  authStub{identity: admintest.Platform(9)},
		Store: store, Enricher: projectenrich.New(resolver),
	})

	request := httptest.NewRequest(http.MethodPost, "/provider-accounts/77/credentials/201/resolve-project", strings.NewReader(`{"tenant_id":8}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound && response.Code != http.StatusForbidden {
		t.Fatalf("篡改 tenant status=%d，期望 404/403，body=%s", response.Code, response.Body.String())
	}
	if resolver.calls != 0 || store.saveCalls != 0 {
		t.Fatalf("越权请求触发解析或写入：resolver=%d save=%d", resolver.calls, store.saveCalls)
	}
}

func TestResellerResolveProjectCredentialSurfaceForbidden(t *testing.T) {
	actors := []struct {
		name     string
		identity admin.AdminIdentity
	}{
		{"子租户 token", admintest.Reseller(51, 10)},
		{"子租户 session", admintest.ResellerSession(61, 10)},
	}
	for _, actor := range actors {
		t.Run(actor.name, func(t *testing.T) {
			store := &storeStub{}
			resolver := &resolverStub{projectRef: "must-not-resolve"}
			audit := &auditStub{}
			handler := mountedHandler(Deps{
				Auth: authStub{identity: actor.identity}, Store: store,
				Enricher: projectenrich.New(resolver), Audit: audit,
			})
			request := httptest.NewRequest(http.MethodPost,
				"/provider-accounts/77/credentials/201/resolve-project",
				strings.NewReader(`{"tenant_id":10}`))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("破坏点→删除 project credential 平台守卫时本断言转红：status=%d body=%s", response.Code, response.Body.String())
			}
			if store.loadCalls != 0 || store.saveCalls != 0 || resolver.calls != 0 || len(audit.params) != 0 {
				t.Fatalf("403 后仍触达凭证面：load=%d save=%d resolve=%d audit=%d",
					store.loadCalls, store.saveCalls, resolver.calls, len(audit.params))
			}
		})
	}
}

func TestResolveProjectUpstreamFailureDoesNotSave(t *testing.T) {
	store := &storeStub{record: credentialstore.CredentialRecord{
		ID: 201, TenantID: 7, ProviderAccountID: 77,
		Vendor: credentialstore.VendorAntigravity, AuthMode: credentialstore.AuthModeOAuth,
		PlaintextPayload: []byte(`{"access_token":"access-secret"}`),
	}}
	resolver := &resolverStub{err: errors.New("上游暂不可用")}
	handler := mountedHandler(Deps{
		Auth:  authStub{identity: admintest.Platform(9)},
		Store: store, Enricher: projectenrich.New(resolver),
	})

	request := httptest.NewRequest(http.MethodPost, "/provider-accounts/77/credentials/201/resolve-project", strings.NewReader(`{"tenant_id":7}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d，期望 502，body=%s", response.Code, response.Body.String())
	}
	if resolver.calls != 1 || store.saveCalls != 0 {
		t.Fatalf("失败路径调用不符：resolver=%d save=%d", resolver.calls, store.saveCalls)
	}
	if strings.Contains(response.Body.String(), "access-secret") {
		t.Fatalf("失败响应泄漏凭据：%s", response.Body.String())
	}
	assertZeroized(t, store.loadedPayload, "失败路径解密载荷")
}

func TestResolveProjectZeroizesEnrichedPayloadOnFailure(t *testing.T) {
	store := &storeStub{record: credentialstore.CredentialRecord{
		ID: 201, TenantID: 7, ProviderAccountID: 77,
		Vendor: credentialstore.VendorAntigravity, AuthMode: credentialstore.AuthModeOAuth,
		PlaintextPayload: []byte(`{"access_token":"access-secret"}`),
	}}
	enricher := &failureEnricherStub{}
	handler := mountedHandler(Deps{
		Auth:  authStub{identity: admintest.Platform(9)},
		Store: store, Enricher: enricher,
	})

	request := httptest.NewRequest(http.MethodPost, "/provider-accounts/77/credentials/201/resolve-project", strings.NewReader(`{"tenant_id":7}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d，期望 502", response.Code)
	}
	assertZeroized(t, enricher.payload, "失败路径补齐载荷")
}

func TestResolveProjectRouteIsSessionSafe(t *testing.T) {
	handler := mountedHandler(Deps{
		Auth:  adminsessionauthtest.Resolver(),
		Store: &storeStub{}, Enricher: projectenrich.New(&resolverStub{}),
	})
	status := adminsessionauthtest.Status(handler, http.MethodPost, "/provider-accounts/77/credentials/201/resolve-project", adminsessionauthtest.SessionBearer)
	if status == http.StatusUnauthorized {
		t.Fatal("resolve-project 必须允许 SessionSafe 管理员写入")
	}
}

func mountedHandler(deps Deps) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Route("/provider-accounts", func(r chi.Router) {
		MountRoutes(r, deps)
	})
	return router
}

func assertZeroized(t *testing.T, value []byte, name string) {
	t.Helper()
	for _, b := range value {
		if b != 0 {
			t.Fatalf("%s 未清零", name)
		}
	}
}
