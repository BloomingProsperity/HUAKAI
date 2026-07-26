package voucherhttp

import (
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

func TestVoucherAdminWriteUsesOwnedTenantBoundary(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name             string
		identity         admin.AdminIdentity
		platformTenantID int64
		targetTenantID   int64
		wantStatus       int
	}{
		{
			name:             "部署者不可给下级租户建券",
			identity:         admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 1},
			platformTenantID: 1,
			targetTenantID:   2,
			wantStatus:       http.StatusForbidden,
		},
		{
			name:           "租户管理员可给本租户建券",
			identity:       admin.AdminIdentity{Role: admin.RoleTenantOperator, TokenID: 2, ScopeTenantID: 2},
			targetTenantID: 2,
			wantStatus:     http.StatusCreated,
		},
		{
			name:           "租户管理员不可给其他租户建券",
			identity:       admin.AdminIdentity{Role: admin.RoleTenantOperator, TokenID: 2, ScopeTenantID: 2},
			targetTenantID: 3,
			wantStatus:     http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := voucher.NewService(voucher.NewMemoryStore())
			r := chi.NewRouter()
			r.Route("/vouchers", func(r chi.Router) {
				MountVoucherAdminRoutes(r, VoucherAdminDeps{
					Auth:             staticVoucherAdminAuth{ident: tt.identity},
					Service:          svc,
					PlatformTenantID: tt.platformTenantID,
				})
			})
			rec := doVoucherRequest(t, r, http.MethodPost, "/vouchers", map[string]any{
				"tenant_id": tt.targetTenantID, "code": "scope-code", "amount_cents": 100,
				"valid_from": now.Add(-time.Minute), "valid_until": now.Add(time.Hour),
			})
			if rec.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			rows, err := svc.List(t.Context(), voucher.ListInput{TenantID: tt.targetTenantID, Limit: 10})
			if err != nil {
				t.Fatalf("list vouchers: %v", err)
			}
			wantCount := 0
			if tt.wantStatus == http.StatusCreated {
				wantCount = 1
			}
			if len(rows) != wantCount {
				t.Fatalf("persisted vouchers=%d want=%d", len(rows), wantCount)
			}
		})
	}
}
