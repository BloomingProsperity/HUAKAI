package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
)

type l2AdminAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (a l2AdminAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.ident, nil
}

func TestAdminL2CacheStatsAndDelete(t *testing.T) {
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	store.Set(context.Background(), l2cache.Entry{Key: "l2:v1:test", TenantID: 7, Vendor: "openai", Model: "gpt-4o", Status: 200, Body: []byte("body")})
	r := chi.NewRouter()
	MountAdminL2CacheRoutes(r, AdminL2CacheDeps{
		Auth:  l2AdminAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store: store,
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", strings.NewReader("")))
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"key":"l2:v1:test"`) {
		t.Fatalf("stats body missing entry: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/l2:v1:test", strings.NewReader("")))
	assertStatus(t, rec, http.StatusOK)
	if _, ok := store.Get(context.Background(), "l2:v1:test"); ok {
		t.Fatal("entry should be deleted")
	}
}

func TestAdminL2CacheTenantOperatorScope(t *testing.T) {
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	store.Set(context.Background(), l2cache.Entry{Key: "tenant-7", TenantID: 7, Vendor: "openai", Model: "gpt-4o", Status: 200, Body: []byte("body")})
	store.Set(context.Background(), l2cache.Entry{Key: "tenant-8", TenantID: 8, Vendor: "openai", Model: "gpt-4o", Status: 200, Body: []byte("body")})
	r := chi.NewRouter()
	MountAdminL2CacheRoutes(r, AdminL2CacheDeps{
		Auth:  l2AdminAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store: store,
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", strings.NewReader("")))
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, `"key":"tenant-7"`) || strings.Contains(body, `"key":"tenant-8"`) {
		t.Fatalf("tenant-scoped stats wrong: %s", body)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/tenant-8", strings.NewReader("")))
	assertStatus(t, rec, http.StatusForbidden)
}

func TestAdminL2CacheUnauthorized(t *testing.T) {
	r := chi.NewRouter()
	MountAdminL2CacheRoutes(r, AdminL2CacheDeps{
		Auth:  l2AdminAuthStub{err: admin.ErrAdminUnauthorized},
		Store: l2cache.NewMemoryStore(1<<20, time.Minute),
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", strings.NewReader("")))
	assertStatus(t, rec, http.StatusUnauthorized)
}
