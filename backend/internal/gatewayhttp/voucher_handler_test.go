package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

func TestVoucherHandlersAdminAndUserRoutes(t *testing.T) {
	now := time.Now().UTC()
	store := voucher.NewMemoryStore()
	svc := voucher.NewService(store)
	auth := staticVoucherAdminAuth{ident: admin.AdminIdentity{TokenID: 99, Role: admin.RolePlatformAdmin}}

	r := chi.NewRouter()
	r.Route("/v1/admin/vouchers", func(r chi.Router) {
		MountVoucherAdminRoutes(r, VoucherAdminDeps{Auth: auth, Service: svc})
	})
	r.Route("/v1/users/me/vouchers", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 1, UserID: 42})
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
		MountVoucherUserRoutes(r, VoucherUserDeps{Service: svc})
	})

	createBody := map[string]any{
		"tenant_id": 1, "code": "handler-code", "amount_cents": 321,
		"valid_from":  now.Add(-time.Minute).Format(time.RFC3339),
		"valid_until": now.Add(time.Hour).Format(time.RFC3339),
	}
	createResp := doVoucherRequest(t, r, http.MethodPost, "/v1/admin/vouchers", createBody)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	if strings.Contains(createResp.Body.String(), "code_hash") {
		t.Fatalf("create response exposed code hash: %s", createResp.Body.String())
	}

	redeemResp := doVoucherRequest(t, r, http.MethodPost, "/v1/users/me/vouchers/redeem", map[string]any{
		"code": "handler-code", "idempotency_key": "handler-idem",
	})
	if redeemResp.Code != http.StatusOK {
		t.Fatalf("redeem status=%d body=%s", redeemResp.Code, redeemResp.Body.String())
	}
	var body struct {
		BalanceCents int64 `json:"balance_cents"`
	}
	if err := json.Unmarshal(redeemResp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode redeem response: %v", err)
	}
	if body.BalanceCents != 321 {
		t.Fatalf("balance_cents=%d, want 321", body.BalanceCents)
	}

	listResp := doVoucherRequest(t, r, http.MethodGet, "/v1/admin/vouchers?tenant_id=1", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResp.Code, listResp.Body.String())
	}
}

func TestAT_BILL_002_011_VoucherGetBatchRouteTenantScoped(t *testing.T) {
	now := time.Now().UTC()
	store := voucher.NewMemoryStore()
	svc := voucher.NewService(store)
	auth := staticVoucherAdminAuth{ident: admin.AdminIdentity{TokenID: 99, Role: admin.RolePlatformAdmin}}

	r := chi.NewRouter()
	r.Route("/v1/admin/vouchers", func(r chi.Router) {
		MountVoucherAdminRoutes(r, VoucherAdminDeps{Auth: auth, Service: svc})
	})

	createResp := doVoucherRequest(t, r, http.MethodPost, "/v1/admin/vouchers/batch", map[string]any{
		"tenant_id": 1, "count": 2, "amount_cents": 500,
		"valid_from":  now.Add(-time.Minute).Format(time.RFC3339),
		"valid_until": now.Add(time.Hour).Format(time.RFC3339),
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create batch status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	var created voucher.BatchCreateResult
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create batch response: %v", err)
	}
	if created.Batch.ID == 0 || len(created.Vouchers) != 2 {
		t.Fatalf("created batch mismatch: %+v", created)
	}

	detailResp := doVoucherRequest(t, r, http.MethodGet, "/v1/admin/vouchers/batches/"+strconv.FormatInt(created.Batch.ID, 10)+"?tenant_id=1", nil)
	if detailResp.Code != http.StatusOK {
		t.Fatalf("get batch status=%d body=%s", detailResp.Code, detailResp.Body.String())
	}
	var detail voucher.GetBatchResult
	if err := json.Unmarshal(detailResp.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode get batch response: %v", err)
	}
	if detail.Batch.ID != created.Batch.ID || detail.Batch.TenantID != 1 || len(detail.Vouchers) != 2 {
		t.Fatalf("batch detail mismatch: %+v", detail)
	}

	crossTenantResp := doVoucherRequest(t, r, http.MethodGet, "/v1/admin/vouchers/batches/"+strconv.FormatInt(created.Batch.ID, 10)+"?tenant_id=2", nil)
	if crossTenantResp.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant status=%d body=%s", crossTenantResp.Code, crossTenantResp.Body.String())
	}
}

func doVoucherRequest(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer hk_admin_test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type staticVoucherAdminAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (a staticVoucherAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.ident, nil
}
