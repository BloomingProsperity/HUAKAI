package mebillinghttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// fakeService 返回固定数据;订单刻意填满管理员内部字段,用于断言它们【不外泄】。
type fakeService struct {
	gotTenant, gotUser int64
	gotLimit           int
}

func (f *fakeService) GetBalance(_ context.Context, tenantID, userID int64) (payment.Balance, error) {
	f.gotTenant, f.gotUser = tenantID, userID
	return payment.Balance{TenantID: tenantID, UserID: userID, AmountCents: 1240}, nil
}

func (f *fakeService) ListOrders(_ context.Context, tenantID, userID int64, limit int) ([]payment.Order, error) {
	f.gotTenant, f.gotUser, f.gotLimit = tenantID, userID, limit
	return []payment.Order{{
		ID: 9, TenantID: tenantID, UserID: userID, OutTradeNo: "T-9",
		AmountCents: 5000, CurrencyCode: "USD", Status: payment.OrderStatus("completed"),
		ProviderKind: payment.ProviderKind("manual"), OrderKind: payment.OrderKindTopup,
		// 管理员内部字段——必须被投影剔除,不能出现在响应里:
		CreatedByAdminID: 777, ConfirmedByAdminID: 888, ConfirmReason: "内部备注", RequestFingerprint: "fp-secret",
		CreatedAt: time.Date(2026, 6, 25, 8, 0, 0, 0, time.UTC),
	}}, nil
}

func serve(t *testing.T, d Deps, withSession bool, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	if withSession {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 7, UserID: 41})
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
	}
	MountRoutes(r, d)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestBalance_ReturnsCurrentUserBalance(t *testing.T) {
	svc := &fakeService{}
	rec := serve(t, Deps{Service: svc}, true, "/balance")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var got balanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// 判别核心:身份来自 session(7/41),不是请求体;金额分→元格式正确。
	if svc.gotTenant != 7 || svc.gotUser != 41 {
		t.Fatalf("身份未从 session 取: tenant=%d user=%d", svc.gotTenant, svc.gotUser)
	}
	if got.BalanceCents != 1240 || got.Balance != "12.40" {
		t.Fatalf("余额错: cents=%d display=%q (期望 1240 / 12.40)", got.BalanceCents, got.Balance)
	}
}

func TestOrders_UserSafeProjection_NoAdminLeak(t *testing.T) {
	svc := &fakeService{}
	rec := serve(t, Deps{Service: svc}, true, "/orders?limit=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	body := rec.Body.String()
	// 判别核心(安全):管理员内部字段绝不能出现在用户响应里。
	// 变异(toUserView 直接序列化整个 Order)→ 这些值会出现 → RED。
	for _, leak := range []string{"777", "888", "内部备注", "fp-secret", "created_by", "confirmed_by", "request_fingerprint"} {
		if strings.Contains(body, leak) {
			t.Fatalf("管理员内部字段外泄: 命中 %q\n响应=%s", leak, body)
		}
	}
	var got ordersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 1 || len(got.Orders) != 1 {
		t.Fatalf("订单数错: %d", got.Count)
	}
	o := got.Orders[0]
	if o.ID != 9 || o.OutTradeNo != "T-9" || o.AmountCents != 5000 || o.Status != "completed" || o.OrderKind != "topup" {
		t.Fatalf("订单投影错: %+v", o)
	}
	if svc.gotLimit != 10 {
		t.Fatalf("limit 未透传: %d", svc.gotLimit)
	}
}

func TestOrders_DefaultLimit(t *testing.T) {
	svc := &fakeService{}
	serve(t, Deps{Service: svc}, true, "/orders")
	if svc.gotLimit != defaultOrderLimit {
		t.Fatalf("默认 limit 错: %d 期望 %d", svc.gotLimit, defaultOrderLimit)
	}
}

func TestNoSession_401(t *testing.T) {
	// 判别核心:无 session 身份必须 401,绝不放行匿名读余额/订单。
	for _, p := range []string{"/balance", "/orders"} {
		rec := serve(t, Deps{Service: &fakeService{}}, false, p)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s 无 session 应 401,得 %d", p, rec.Code)
		}
	}
}

func TestCentsToDisplay(t *testing.T) {
	cases := map[int64]string{0: "0.00", 5: "0.05", 1240: "12.40", 100000: "1000.00", -250: "-2.50"}
	for cents, want := range cases {
		if got := centsToDisplay(cents); got != want {
			t.Fatalf("centsToDisplay(%d)=%q 期望 %q", cents, got, want)
		}
	}
}
