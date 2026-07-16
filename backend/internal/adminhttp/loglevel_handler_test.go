package adminhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap/zapcore"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	"github.com/BloomingProsperity/HUAKAI/internal/loglevel"
)

type stubLogLevelAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (s stubLogLevelAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

func buildLogLevelRouter(d LogLevelDeps) *chi.Mux {
	r := chi.NewRouter()
	MountLogLevelRoutes(r, d)
	return r
}

func TestLogLevel_GetReturnsCurrent_PlatformAdmin(t *testing.T) {
	loglevel.Level.SetLevel(zapcore.InfoLevel)
	r := buildLogLevelRouter(LogLevelDeps{Auth: stubLogLevelAuth{ident: admintest.Platform(0)}})
	req := httptest.NewRequest(http.MethodGet, "/loglevel", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "info") {
		t.Fatalf("GET body=%q want current level info", rec.Body.String())
	}
}

func TestLogLevel_PutSetsGlobalLevel_PlatformAdmin(t *testing.T) {
	loglevel.Level.SetLevel(zapcore.InfoLevel)
	defer loglevel.Level.SetLevel(zapcore.InfoLevel)
	r := buildLogLevelRouter(LogLevelDeps{Auth: stubLogLevelAuth{ident: admintest.Platform(0)}})
	req := httptest.NewRequest(http.MethodPut, "/loglevel", strings.NewReader(`{"level":"debug"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	// 变异防护:如果 handler 委托给一个临时丢弃的 AtomicLevel,
	// 而不是真正进程级的 loglevel.Level,全局级别会停留在
	// info,这条断言就会变红。
	if loglevel.Level.Level() != zapcore.DebugLevel {
		t.Fatalf("after PUT, global level=%v want debug", loglevel.Level.Level())
	}
}

func TestLogLevel_ForbiddenForNonPlatformAdmin(t *testing.T) {
	r := buildLogLevelRouter(LogLevelDeps{Auth: stubLogLevelAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator}}})
	req := httptest.NewRequest(http.MethodGet, "/loglevel", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant_operator status=%d want 403", rec.Code)
	}
}

func TestLogLevel_NilDeps503(t *testing.T) {
	r := buildLogLevelRouter(LogLevelDeps{})
	req := httptest.NewRequest(http.MethodGet, "/loglevel", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil deps status=%d want 503", rec.Code)
	}
}
