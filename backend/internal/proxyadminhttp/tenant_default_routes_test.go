package proxyadminhttp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/proxyadmin"
)

type tenantDefaultProxyFixture struct {
	tenantID int64
	deleted  bool
}

type tenantDefaultStoreStub struct {
	values    map[int64]*int64
	proxies   map[int64]tenantDefaultProxyFixture
	setCalls  int
	lastAudit proxyadmin.TenantDefaultAudit
}

func (s *tenantDefaultStoreStub) Get(_ context.Context, tenantID int64) (proxyadmin.TenantDefaultProxy, error) {
	value, exists := s.values[tenantID]
	if !exists {
		return proxyadmin.TenantDefaultProxy{}, proxyadmin.ErrTenantNotFound
	}
	return proxyadmin.TenantDefaultProxy{ProxyID: cloneInt64(value)}, nil
}

func (s *tenantDefaultStoreStub) Set(_ context.Context, tenantID int64, proxyID *int64, audit proxyadmin.TenantDefaultAudit) (proxyadmin.TenantDefaultProxy, error) {
	s.setCalls++
	s.lastAudit = audit
	if _, exists := s.values[tenantID]; !exists {
		return proxyadmin.TenantDefaultProxy{}, proxyadmin.ErrTenantNotFound
	}
	if proxyID != nil {
		proxy, exists := s.proxies[*proxyID]
		if !exists || proxy.tenantID != tenantID || proxy.deleted {
			return proxyadmin.TenantDefaultProxy{}, proxyadmin.ErrNotFound
		}
	}
	s.values[tenantID] = cloneInt64(proxyID)
	return proxyadmin.TenantDefaultProxy{ProxyID: cloneInt64(proxyID)}, nil
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func invokeTenantDefault(t *testing.T, d Deps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/tenants", func(r chi.Router) { MountTenantRoutes(r, d) })
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("X-Request-ID", "req-tenant-default")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestTenantDefaultProxySetThenReadBack 是写口 CRUD 主路径。变异：PUT 漏调 Set、
// GET 写死 null、path tenant 未透传或响应漏 proxy_id，均会转红。
func TestTenantDefaultProxySetThenReadBack(t *testing.T) {
	store := &tenantDefaultStoreStub{
		values:  map[int64]*int64{7: nil},
		proxies: map[int64]tenantDefaultProxyFixture{41: {tenantID: 7}},
	}
	d := Deps{Auth: authStub{ident: tenantOperator(7)}, TenantDefaults: store}

	put := invokeTenantDefault(t, d, http.MethodPut, "/admin/v1/tenants/7/default-proxy", `{"proxy_id":41}`)
	assertStatus(t, put, http.StatusOK)
	var setBody struct {
		ProxyID *int64 `json:"proxy_id"`
	}
	decodeBody(t, put, &setBody)
	if setBody.ProxyID == nil || *setBody.ProxyID != 41 {
		t.Fatalf("PUT proxy_id=%v want 41", setBody.ProxyID)
	}

	get := invokeTenantDefault(t, d, http.MethodGet, "/admin/v1/tenants/7/default-proxy", "")
	assertStatus(t, get, http.StatusOK)
	var getBody struct {
		ProxyID *int64 `json:"proxy_id"`
	}
	decodeBody(t, get, &getBody)
	if getBody.ProxyID == nil || *getBody.ProxyID != 41 {
		t.Fatalf("GET proxy_id=%v want 41", getBody.ProxyID)
	}
	if store.setCalls != 1 || store.lastAudit.ActorID != "admin_token:1" ||
		store.lastAudit.ActorRole != "tenant_operator" || store.lastAudit.RequestID != "req-tenant-default" {
		t.Fatalf("Set/audit 未精确透传: calls=%d audit=%+v", store.setCalls, store.lastAudit)
	}
}

// TestTenantDefaultProxyRejectsCrossTenantAndSoftDeleted 钉住存在性防手滑：两类代理
// 都折叠成 404，且原默认值不变。删除 store 校验或传错 tenant 会转红。
func TestTenantDefaultProxyRejectsCrossTenantAndSoftDeleted(t *testing.T) {
	original := int64(11)
	cases := []struct {
		name    string
		proxyID int64
		proxy   tenantDefaultProxyFixture
	}{
		{name: "跨租户", proxyID: 81, proxy: tenantDefaultProxyFixture{tenantID: 8}},
		{name: "已软删", proxyID: 82, proxy: tenantDefaultProxyFixture{tenantID: 7, deleted: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &tenantDefaultStoreStub{
				values:  map[int64]*int64{7: cloneInt64(&original)},
				proxies: map[int64]tenantDefaultProxyFixture{tc.proxyID: tc.proxy},
			}
			d := Deps{Auth: authStub{ident: tenantOperator(7)}, TenantDefaults: store}
			rec := invokeTenantDefault(t, d, http.MethodPut, "/admin/v1/tenants/7/default-proxy",
				fmt.Sprintf(`{"proxy_id":%d}`, tc.proxyID))
			assertStatus(t, rec, http.StatusNotFound)
			if got := store.values[7]; got == nil || *got != original {
				t.Fatalf("拒绝后默认值=%v want %d", got, original)
			}
		})
	}
}

// TestTenantDefaultProxyNullClears 钉住 JSON null 而非 0/缺字段的清除契约。
func TestTenantDefaultProxyNullClears(t *testing.T) {
	original := int64(11)
	store := &tenantDefaultStoreStub{values: map[int64]*int64{7: &original}}
	d := Deps{Auth: authStub{ident: tenantOperator(7)}, TenantDefaults: store}
	rec := invokeTenantDefault(t, d, http.MethodPut, "/admin/v1/tenants/7/default-proxy", `{"proxy_id":null}`)
	assertStatus(t, rec, http.StatusOK)
	var body map[string]any
	decodeBody(t, rec, &body)
	value, exists := body["proxy_id"]
	if !exists || value != nil || store.values[7] != nil {
		t.Fatalf("清除结果 exists=%v value=%v stored=%v", exists, value, store.values[7])
	}
}

func TestTenantDefaultProxyValidatesBodyAndTenantScope(t *testing.T) {
	store := &tenantDefaultStoreStub{values: map[int64]*int64{7: nil}}
	t.Run("缺 proxy_id", func(t *testing.T) {
		rec := invokeTenantDefault(t, Deps{Auth: authStub{ident: tenantOperator(7)}, TenantDefaults: store},
			http.MethodPut, "/admin/v1/tenants/7/default-proxy", `{}`)
		assertStatus(t, rec, http.StatusBadRequest)
	})
	t.Run("非正整数", func(t *testing.T) {
		rec := invokeTenantDefault(t, Deps{Auth: authStub{ident: tenantOperator(7)}, TenantDefaults: store},
			http.MethodPut, "/admin/v1/tenants/7/default-proxy", `{"proxy_id":0}`)
		assertStatus(t, rec, http.StatusBadRequest)
	})
	t.Run("租户 operator 越权", func(t *testing.T) {
		rec := invokeTenantDefault(t, Deps{Auth: authStub{ident: tenantOperator(7)}, TenantDefaults: store},
			http.MethodGet, "/admin/v1/tenants/8/default-proxy", "")
		assertStatus(t, rec, http.StatusForbidden)
	})
	if store.setCalls != 0 {
		t.Fatalf("非法请求不应触达 Set，calls=%d", store.setCalls)
	}
}

func TestTenantDefaultProxyBackendErrorMapping(t *testing.T) {
	store := tenantDefaultFailStore{err: proxyadmin.ErrBackend}
	rec := invokeTenantDefault(t, Deps{Auth: authStub{ident: tenantOperator(7)}, TenantDefaults: store},
		http.MethodGet, "/admin/v1/tenants/7/default-proxy", "")
	assertStatus(t, rec, http.StatusServiceUnavailable)
}

type tenantDefaultFailStore struct{ err error }

func (s tenantDefaultFailStore) Get(context.Context, int64) (proxyadmin.TenantDefaultProxy, error) {
	return proxyadmin.TenantDefaultProxy{}, s.err
}
func (s tenantDefaultFailStore) Set(context.Context, int64, *int64, proxyadmin.TenantDefaultAudit) (proxyadmin.TenantDefaultProxy, error) {
	return proxyadmin.TenantDefaultProxy{}, s.err
}
