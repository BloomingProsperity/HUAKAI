package accountfphttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type authStub struct {
	ident admin.AdminIdentity
	err   error
}

func (a authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.ident, nil
}

type storeStub struct {
	bind    *admindb.UpdateProviderAccountFingerprintProfileParams
	bindErr error
	audits  []admindb.InsertAdminAuditEventParams
}

func (s *storeStub) UpdateFingerprintProfileWithAudit(
	_ context.Context,
	arg admindb.UpdateProviderAccountFingerprintProfileParams,
	audit admindb.InsertAdminAuditEventParams,
) error {
	s.bind = &arg
	if s.bindErr != nil {
		return s.bindErr
	}
	s.audits = append(s.audits, audit)
	return nil
}

func platformAdmin() admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 99, Role: admin.RolePlatformAdmin}
}
func tenantOp(tid int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 1, Role: admin.RoleTenantOperator, ScopeTenantID: tid}
}

func invoke(d Deps, target, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) { MountRoutes(r, d) })
	req := httptest.NewRequest(http.MethodPatch, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestBindFingerprintProfile(t *testing.T) {
	store := &storeStub{}
	rec := invoke(Deps{Auth: authStub{ident: platformAdmin()}, Store: store},
		"/admin/v1/provider-accounts/77/fingerprint-profile?tenant_id=7", `{"profile_id":5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200;体=%s", rec.Code, rec.Body.String())
	}
	if store.bind == nil || store.bind.ID != 77 || store.bind.TenantID != 7 ||
		store.bind.ProfileID == nil || *store.bind.ProfileID != 5 {
		t.Fatalf("绑定参数不符: %+v", store.bind)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "update_provider_account" {
		t.Fatalf("审计不符: %+v", store.audits)
	}
}

func TestUnbindFingerprintProfile(t *testing.T) {
	store := &storeStub{}
	rec := invoke(Deps{Auth: authStub{ident: platformAdmin()}, Store: store},
		"/admin/v1/provider-accounts/77/fingerprint-profile?tenant_id=7", `{"profile_id":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200", rec.Code)
	}
	if store.bind == nil || store.bind.ProfileID != nil {
		t.Fatalf("解绑应传 nil profile_id(回内置默认): %+v", store.bind)
	}
}

// 非法 profile_id(<=0)在触达 store 前被 400 拦下(变异:删 <=0 校验 → 0 会被下发)。
func TestInvalidProfileIDRejected(t *testing.T) {
	store := &storeStub{}
	rec := invoke(Deps{Auth: authStub{ident: platformAdmin()}, Store: store},
		"/admin/v1/provider-accounts/77/fingerprint-profile?tenant_id=7", `{"profile_id":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("profile_id=0 应 400,实得 %d", rec.Code)
	}
	if store.bind != nil {
		t.Fatal("非法 profile_id 不应触达 store")
	}
}

// 跨租户/不存在 profile:DB 触发器 P0001 / FK 23503 → 映射 400 而非 503。
func TestCrossTenantProfileMapsTo400(t *testing.T) {
	store := &storeStub{bindErr: &pgconn.PgError{Code: "P0001", Message: "profile does not belong to tenant"}}
	rec := invoke(Deps{Auth: authStub{ident: platformAdmin()}, Store: store},
		"/admin/v1/provider-accounts/77/fingerprint-profile?tenant_id=7", `{"profile_id":999}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("跨租户 profile(P0001)应 400,实得 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_fingerprint_profile") {
		t.Fatalf("应 invalid_fingerprint_profile,实得 %s", rec.Body.String())
	}
}

// IDOR 守卫:租户运营者 scope=7,body 显式 tenant_id=9(他人)→ 403,且不触达 store。
func TestTenantMismatchRejected(t *testing.T) {
	store := &storeStub{}
	rec := invoke(Deps{Auth: authStub{ident: tenantOp(7)}, Store: store},
		"/admin/v1/provider-accounts/77/fingerprint-profile", `{"tenant_id":9,"profile_id":5}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("body tenant_id 与 scope 不符应 403,实得 %d", rec.Code)
	}
	if store.bind != nil {
		t.Fatal("越权时不应触达 store")
	}
}

// platform_admin 不传 ?tenant_id → 400(不会默认成某租户)。
func TestPlatformAdminRequiresTenantID(t *testing.T) {
	store := &storeStub{}
	rec := invoke(Deps{Auth: authStub{ident: platformAdmin()}, Store: store},
		"/admin/v1/provider-accounts/77/fingerprint-profile", `{"profile_id":5}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("平台 admin 缺 tenant_id 应 400,实得 %d", rec.Code)
	}
	if store.bind != nil {
		t.Fatal("缺 tenant_id 时不应触达 store")
	}
}

// 超大请求体(>64KiB)被 MaxBytesReader 拦下 → 400 invalid_json,不触达 store。
// 变异:删 decodeJSON 里的 http.MaxBytesReader 行 → Decode 吞下整个体、本测转 200 而红。
func TestOversizedBodyRejected(t *testing.T) {
	store := &storeStub{}
	huge := strings.Repeat("A", 1<<17) // 128KiB,远超 64KiB 上限
	body := `{"profile_id":5,"reason":"` + huge + `"}`
	rec := invoke(Deps{Auth: authStub{ident: platformAdmin()}, Store: store},
		"/admin/v1/provider-accounts/77/fingerprint-profile?tenant_id=7", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("超大体应 400,实得 %d", rec.Code)
	}
	if store.bind != nil {
		t.Fatal("超大体被拦时不应触达 store")
	}
}

func TestUnauthorizedRejected(t *testing.T) {
	store := &storeStub{}
	rec := invoke(Deps{Auth: authStub{err: admin.ErrAdminUnauthorized}, Store: store},
		"/admin/v1/provider-accounts/77/fingerprint-profile?tenant_id=7", `{"profile_id":5}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("鉴权失败应 401,实得 %d", rec.Code)
	}
	if store.bind != nil {
		t.Fatal("鉴权失败不应触达 store")
	}
}
