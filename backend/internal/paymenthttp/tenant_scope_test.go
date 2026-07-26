package paymenthttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func paymentAdminRouterForScope(ident admin.AdminIdentity, platformTenantID int64, svc *captureService) http.Handler {
	r := chi.NewRouter()
	r.Route("/payments", func(r chi.Router) {
		MountPaymentAdminRoutes(r, AdminDeps{
			Auth:             fakeAdminAuth{ident: ident},
			Service:          svc,
			PlatformTenantID: platformTenantID,
		})
	})
	return r
}

func TestPaymentAdminWriteUsesOwnedTenantBoundary(t *testing.T) {
	tests := []struct {
		name             string
		identity         admin.AdminIdentity
		platformTenantID int64
		targetTenantID   int64
		wantStatus       int
		wantCalled       bool
	}{
		{
			name:             "部署者可给平台租户建单",
			identity:         admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 1},
			platformTenantID: 5,
			targetTenantID:   5,
			wantStatus:       http.StatusCreated,
			wantCalled:       true,
		},
		{
			name:             "部署者不可代下级租户建单",
			identity:         admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 1},
			platformTenantID: 5,
			targetTenantID:   6,
			wantStatus:       http.StatusForbidden,
		},
		{
			name:           "租户管理员可给本租户建单",
			identity:       admin.AdminIdentity{Role: admin.RoleTenantOperator, TokenID: 2, ScopeTenantID: 6},
			targetTenantID: 6,
			wantStatus:     http.StatusCreated,
			wantCalled:     true,
		},
		{
			name:           "租户管理员不可跨租户建单",
			identity:       admin.AdminIdentity{Role: admin.RoleTenantOperator, TokenID: 2, ScopeTenantID: 6},
			targetTenantID: 7,
			wantStatus:     http.StatusForbidden,
		},
		{
			name:           "部署者工作租户未接线时失败关闭",
			identity:       admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 1},
			targetTenantID: 5,
			wantStatus:     http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &captureService{createRes: payment.CreateOrderResult{Order: payment.Order{ID: 10}}}
			h := paymentAdminRouterForScope(tt.identity, tt.platformTenantID, svc)
			body := []byte(`{"tenant_id":` + strconv.FormatInt(tt.targetTenantID, 10) + `,"user_id":9,"amount_cents":100}`)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/payments/", bytes.NewReader(body)))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := svc.gotCreate.TenantID != 0; got != tt.wantCalled {
				t.Fatalf("service called=%v want=%v input=%+v", got, tt.wantCalled, svc.gotCreate)
			}
		})
	}
}

func TestPaymentAdminReadKeepsPlatformOversightButScopesTenantOperator(t *testing.T) {
	platformSvc := &captureService{}
	platform := paymentAdminRouterForScope(
		admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 1},
		5,
		platformSvc,
	)
	rec := httptest.NewRecorder()
	platform.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/payments/?tenant_id=6", nil))
	if rec.Code != http.StatusOK || platformSvc.gotAdminList.TenantID != 6 {
		t.Fatalf("platform cross-tenant read status=%d filter=%+v body=%s", rec.Code, platformSvc.gotAdminList, rec.Body.String())
	}

	tenantSvc := &captureService{}
	tenant := paymentAdminRouterForScope(
		admin.AdminIdentity{Role: admin.RoleTenantOperator, TokenID: 2, ScopeTenantID: 6},
		5,
		tenantSvc,
	)
	rec = httptest.NewRecorder()
	tenant.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/payments/?tenant_id=7", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant cross-tenant read status=%d want=403 body=%s", rec.Code, rec.Body.String())
	}
	if tenantSvc.gotAdminList.TenantID != 0 {
		t.Fatalf("forbidden read reached service: %+v", tenantSvc.gotAdminList)
	}
}
