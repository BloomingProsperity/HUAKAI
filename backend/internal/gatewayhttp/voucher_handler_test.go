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
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
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

// sourceIPCapturingVoucherService records the SourceIP the handler passes into Redeem.
type sourceIPCapturingVoucherService struct{ lastSourceIP string }

func (c *sourceIPCapturingVoucherService) Create(context.Context, voucher.CreateInput) (voucher.CreateResult, error) {
	return voucher.CreateResult{}, nil
}
func (c *sourceIPCapturingVoucherService) CreateBatch(context.Context, voucher.BatchCreateInput) (voucher.BatchCreateResult, error) {
	return voucher.BatchCreateResult{}, nil
}
func (c *sourceIPCapturingVoucherService) Redeem(_ context.Context, in voucher.RedeemInput) (voucher.RedeemResult, error) {
	c.lastSourceIP = in.SourceIP
	return voucher.RedeemResult{BalanceCents: 1}, nil
}
func (c *sourceIPCapturingVoucherService) Revoke(context.Context, voucher.RevokeInput) (voucher.Voucher, error) {
	return voucher.Voucher{}, nil
}
func (c *sourceIPCapturingVoucherService) List(context.Context, voucher.ListInput) ([]voucher.Voucher, error) {
	return nil, nil
}
func (c *sourceIPCapturingVoucherService) GetBatch(context.Context, int64, int64) (voucher.GetBatchResult, error) {
	return voucher.GetBatchResult{}, nil
}

// TestVoucherUserRedeemUsesTrustedProxyClientIP (S2-109) proves the redeem handler routes the
// request through the trusted-proxy-aware ClientIPResolver, not raw RemoteAddr. The socket peer
// (10.1.2.3) is a trusted proxy and the real client (198.51.100.9) is in X-Forwarded-For, so the
// SourceIP recorded for burst/anomaly purposes must be the forwarded client.
//
// Mutation check: revert the call site to RemoteAddr (or drop ClientIPResolver from the deps so the
// nil-safe resolver falls back to the socket peer) → recorded SourceIP becomes "10.1.2.3" → red.
func TestVoucherUserRedeemUsesTrustedProxyClientIP(t *testing.T) {
	svc := &sourceIPCapturingVoucherService{}
	resolver, err := clientip.NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/v1/users/me/vouchers", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 1, UserID: 42})
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
		MountVoucherUserRoutes(r, VoucherUserDeps{Service: svc, ClientIPResolver: resolver})
	})

	payload, err := json.Marshal(map[string]any{"code": "c", "idempotency_key": "k"})
	if err != nil {
		t.Fatal(err)
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/users/me/vouchers/redeem", bytes.NewReader(payload))
	httpReq.RemoteAddr = "10.1.2.3:5000"                  // trusted reverse-proxy peer
	httpReq.Header.Set("X-Forwarded-For", "198.51.100.9") // real client behind it
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("redeem status=%d body=%s", rec.Code, rec.Body.String())
	}
	if svc.lastSourceIP != "198.51.100.9" {
		t.Fatalf("Redeem SourceIP=%q want forwarded client 198.51.100.9 (handler must consult ClientIPResolver, not RemoteAddr)", svc.lastSourceIP)
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
