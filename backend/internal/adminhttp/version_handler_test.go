package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/buildinfo"
)

// invokeVersion 构建一个最小化的 chi router,镜像 routes.go 使用的完全相同
// 的路径,然后向给定的 target 发起一个 GET 请求。
func invokeVersion(t *testing.T, deps VersionDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1", func(r chi.Router) {
		MountVersionRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestVersionUnauthorized:未认证的调用方得到 401。
func TestVersionUnauthorized(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{
		Auth: versionAuthStub{err: admin.ErrAdminUnauthorized},
	}, "/admin/v1/version")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestVersionForbidden:已认证但角色未知,得到 403。
func TestVersionForbidden(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{
		Auth: versionAuthStub{ident: admin.AdminIdentity{Role: "unknown_role"}},
	}, "/admin/v1/version")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestVersionTenantOperatorOK:tenant_operator 看到 200,且包含全部 4 个字段。
func TestVersionTenantOperatorOK(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{
		Auth: versionAuthStub{ident: tenantOperator(7)},
	}, "/admin/v1/version")

	assertVersionOK(t, rec)
}

// TestVersionPlatformAdminOK:platform_admin 同样看到 200。
func TestVersionPlatformAdminOK(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{
		Auth: versionAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
	}, "/admin/v1/version")

	assertVersionOK(t, rec)
}

// TestVersionGoVersionNonEmpty:go_version 字段始终反映 runtime.Version()。
func TestVersionGoVersionNonEmpty(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{
		Auth: versionAuthStub{ident: tenantOperator(1)},
	}, "/admin/v1/version")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body buildinfo.Info
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if body.GoVersion == "" {
		t.Fatal("go_version must not be empty")
	}
	if body.GoVersion != runtime.Version() {
		t.Errorf("go_version = %q, want %q", body.GoVersion, runtime.Version())
	}
}

// TestVersionNilDepsServiceUnavailable:Auth 为 nil → 503(而非 panic)。
func TestVersionNilDepsServiceUnavailable(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{Auth: nil}, "/admin/v1/version")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestVersionRouteNotDoublePrefix:确保 router 在 /admin/v1/version 上响应 200,
// 而在 /admin/v1/admin/v1/version 上不响应(双重前缀守卫)。
func TestVersionRouteNotDoublePrefix(t *testing.T) {
	deps := VersionDeps{Auth: versionAuthStub{ident: tenantOperator(1)}}

	good := invokeVersion(t, deps, "/admin/v1/version")
	if good.Code != http.StatusOK {
		t.Fatalf("correct path: status=%d body=%s", good.Code, good.Body.String())
	}

	// 构建同一个 router,并访问一个错误的双重前缀路径——必须返回 404。
	r := chi.NewRouter()
	r.Route("/admin/v1", func(r chi.Router) {
		MountVersionRoutes(r, deps)
	})
	bad := httptest.NewRecorder()
	r.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/admin/v1/admin/v1/version", nil))
	if bad.Code != http.StatusNotFound {
		t.Fatalf("double-prefix path should 404, got %d", bad.Code)
	}
}

func assertVersionOK(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	for _, field := range []string{"version", "commit", "build_time", "go_version"} {
		if _, ok := body[field]; !ok {
			t.Errorf("response missing field %q; body=%s", field, rec.Body.String())
		}
	}
}

type versionAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s versionAuthStub) Resolve(_ context.Context, _ *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}
