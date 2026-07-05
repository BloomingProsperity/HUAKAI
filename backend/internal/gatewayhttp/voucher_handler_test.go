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
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

// promoSettingsStub 让 Get(promo_enabled) 返回固定值,测 promo 总开关门控。
type promoSettingsStub struct{ value string }

func (s promoSettingsStub) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	return platformsettings.StoredSetting{Key: key, Value: s.value}, nil
}

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

// sourceIPCapturingVoucherService 记录 handler 传入 Redeem 的 SourceIP。
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

// TestVoucherUserRedeemUsesTrustedProxyClientIP 证明 redeem handler 让请求经过
// 能识别可信代理的 ClientIPResolver，而非裸用 RemoteAddr。socket 对端
// (10.1.2.3) 是可信代理，真实客户端 (198.51.100.9) 在 X-Forwarded-For 中，所以
// 出于突发/异常用途记录的 SourceIP 必须是被转发的客户端。
//
// 变异:检查:把调用点改回 RemoteAddr(或从 deps 中去掉 ClientIPResolver，使
// nil-safe 的 resolver 退回到 socket 对端)→ 记录的 SourceIP 变成 "10.1.2.3" → 变红。
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
	httpReq.RemoteAddr = "10.1.2.3:5000"                  // 可信反向代理对端
	httpReq.Header.Set("X-Forwarded-For", "198.51.100.9") // 其后的真实客户端
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

// TestVoucherRedeemPromoGate 验证 promo 总开关(A 方案):运营者显式关闭(promo_enabled="false")
// 时兑换被拦 403 promo_disabled 且码**未被消费**;未配置(nil,行为保持)时兑换照常成功。
// 变异:删 newVoucherRedeemHandler 里的 promoRedeemEnabled 门控 → 关闭时兑换变 200 → 本测试转红。
func TestVoucherRedeemPromoGate(t *testing.T) {
	now := time.Now().UTC()
	store := voucher.NewMemoryStore()
	svc := voucher.NewService(store)
	auth := staticVoucherAdminAuth{ident: admin.AdminIdentity{TokenID: 99, Role: admin.RolePlatformAdmin}}

	sessionMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 1, UserID: 42})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}

	adminR := chi.NewRouter()
	adminR.Route("/v1/admin/vouchers", func(r chi.Router) { MountVoucherAdminRoutes(r, VoucherAdminDeps{Auth: auth, Service: svc}) })
	createResp := doVoucherRequest(t, adminR, http.MethodPost, "/v1/admin/vouchers", map[string]any{
		"tenant_id": 1, "code": "promo-gate-code", "amount_cents": 100,
		"valid_from":  now.Add(-time.Minute).Format(time.RFC3339),
		"valid_until": now.Add(time.Hour).Format(time.RFC3339),
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}

	redeem := func(settings platformSettingsReader, idem string) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		r.Route("/v1/users/me/vouchers", func(r chi.Router) {
			r.Use(sessionMW)
			MountVoucherUserRoutes(r, VoucherUserDeps{Service: svc, PlatformSettings: settings})
		})
		return doVoucherRequest(t, r, http.MethodPost, "/v1/users/me/vouchers/redeem",
			map[string]any{"code": "promo-gate-code", "idempotency_key": idem})
	}

	// promo 显式关闭 → 403 promo_disabled,门控在调 Redeem 前短路(码不被消费)。
	blocked := redeem(promoSettingsStub{value: "false"}, "promo-gate-blocked")
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("promo 关闭时兑换应 403,实得 %d body=%s", blocked.Code, blocked.Body.String())
	}
	if !strings.Contains(blocked.Body.String(), "promo_disabled") {
		t.Fatalf("应返回 promo_disabled,实得 %s", blocked.Body.String())
	}

	// promo 未配置(nil)→ 行为保持,兑换成功(也证明上面被拦时码未被消费)。
	ok := redeem(nil, "promo-gate-ok")
	if ok.Code != http.StatusOK {
		t.Fatalf("promo 默认开启时兑换应成功,实得 %d body=%s", ok.Code, ok.Body.String())
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
