package subscriptionhttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

func TestSubscriptionAdminWriteUsesOwnedTenantBoundary(t *testing.T) {
	tests := []struct {
		name             string
		identity         admin.AdminIdentity
		platformTenantID int64
		targetTenantID   int64
		wantStatus       int
		wantCalled       bool
	}{
		{
			name:             "部署者可修改平台租户套餐",
			identity:         admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 1},
			platformTenantID: 5,
			targetTenantID:   5,
			wantStatus:       http.StatusOK,
			wantCalled:       true,
		},
		{
			name:             "部署者不可修改下级租户套餐",
			identity:         admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 1},
			platformTenantID: 5,
			targetTenantID:   6,
			wantStatus:       http.StatusForbidden,
		},
		{
			name:           "租户管理员可修改本租户套餐",
			identity:       admin.AdminIdentity{Role: admin.RoleTenantOperator, TokenID: 2, ScopeTenantID: 6},
			targetTenantID: 6,
			wantStatus:     http.StatusOK,
			wantCalled:     true,
		},
		{
			name:           "租户管理员不可修改其他租户套餐",
			identity:       admin.AdminIdentity{Role: admin.RoleTenantOperator, TokenID: 2, ScopeTenantID: 6},
			targetTenantID: 7,
			wantStatus:     http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &adminOpsServiceStub{}
			h := newSubAdminTestRouter(AdminDeps{
				Auth:             fakeAdminAuth{ident: tt.identity},
				Service:          svc,
				PlatformTenantID: tt.platformTenantID,
			})
			body := []byte(`{"tenant_id":` + strconv.FormatInt(tt.targetTenantID, 10) + `,"name":"plan","validity_days":30,"for_sale":true}`)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/subs/plans/9", bytes.NewReader(body)))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := svc.gotUpdate.TenantID != 0; got != tt.wantCalled {
				t.Fatalf("service called=%v want=%v input=%+v", got, tt.wantCalled, svc.gotUpdate)
			}
		})
	}
}

func TestSubscriptionAdminReadKeepsPlatformOversight(t *testing.T) {
	svc := &fakeSubscriptionService{}
	h := newSubAdminTestRouter(AdminDeps{
		Auth:             fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 1}},
		Service:          svc,
		PlatformTenantID: 5,
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/subs/plans?tenant_id=6", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("platform cross-tenant read status=%d want=200 body=%s", rec.Code, rec.Body.String())
	}
}
