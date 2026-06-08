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

// invokeVersion builds a minimal chi router that mirrors the exact path used
// by routes.go, then fires a GET request at the given target.
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

// TestVersionUnauthorized: unauthenticated caller gets 401.
func TestVersionUnauthorized(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{
		Auth: versionAuthStub{err: admin.ErrAdminUnauthorized},
	}, "/admin/v1/version")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestVersionForbidden: authenticated but unknown role gets 403.
func TestVersionForbidden(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{
		Auth: versionAuthStub{ident: admin.AdminIdentity{Role: "unknown_role"}},
	}, "/admin/v1/version")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestVersionTenantOperatorOK: tenant_operator sees 200 with all 4 fields.
func TestVersionTenantOperatorOK(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{
		Auth: versionAuthStub{ident: tenantOperator(7)},
	}, "/admin/v1/version")

	assertVersionOK(t, rec)
}

// TestVersionPlatformAdminOK: platform_admin also sees 200.
func TestVersionPlatformAdminOK(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{
		Auth: versionAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
	}, "/admin/v1/version")

	assertVersionOK(t, rec)
}

// TestVersionGoVersionNonEmpty: go_version field always reflects runtime.Version().
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

// TestVersionNilDepsServiceUnavailable: nil Auth → 503 (not panic).
func TestVersionNilDepsServiceUnavailable(t *testing.T) {
	rec := invokeVersion(t, VersionDeps{Auth: nil}, "/admin/v1/version")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestVersionRouteNotDoublePrefix: ensure router responds 200 at /admin/v1/version
// and NOT at /admin/v1/admin/v1/version (double-prefix guard).
func TestVersionRouteNotDoublePrefix(t *testing.T) {
	deps := VersionDeps{Auth: versionAuthStub{ident: tenantOperator(1)}}

	good := invokeVersion(t, deps, "/admin/v1/version")
	if good.Code != http.StatusOK {
		t.Fatalf("correct path: status=%d body=%s", good.Code, good.Body.String())
	}

	// Build the SAME router and hit a bad double-prefix path — must 404.
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
